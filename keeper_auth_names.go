package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type keeperAuthName struct {
	IdentityID  string `json:"identity_id"`
	AuthIndex   string `json:"auth_index"`
	Alias       string `json:"alias"`
	DisplayName string `json:"display_name"`
}

type keeperAuthNamesResponse struct {
	Status            string           `json:"status"`
	Items             []keeperAuthName `json:"items"`
	FetchedAt         string           `json:"fetched_at,omitempty"`
	ErrorCode         string           `json:"error_code,omitempty"`
	RetryAfterSeconds int64            `json:"retry_after_seconds,omitempty"`
}

func keeperNamesUnavailable(code string) keeperAuthNamesResponse {
	return keeperAuthNamesResponse{Status: "unavailable", Items: []keeperAuthName{}, ErrorCode: code}
}

func (c *keeperClient) fetchAuthNames(ctx context.Context) ([]keeperAuthName, error) {
	resp, err := c.authenticatedRequest(ctx, http.MethodGet, "/api/v1/usage/identities", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, keeperHTTPError(resp)
	}
	raw, err := readKeeperBody(ctx, resp.Body)
	if err != nil {
		return nil, err
	}
	return parseKeeperAuthNames(raw)
}

func parseKeeperAuthNames(raw []byte) ([]keeperAuthName, error) {
	var body struct {
		Identities json.RawMessage `json:"identities"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || len(body.Identities) == 0 || string(body.Identities) == "null" {
		return nil, &keeperError{Code: "invalid_response"}
	}
	var rows []struct {
		ID          *string         `json:"id"`
		Identity    *string         `json:"identity"`
		Alias       json.RawMessage `json:"alias"`
		DisplayName *string         `json:"displayName"`
		AuthType    *int            `json:"auth_type"`
		Type        *string         `json:"type"`
		Provider    *string         `json:"provider"`
		Deleted     *bool           `json:"is_deleted"`
	}
	if err := json.Unmarshal(body.Identities, &rows); err != nil {
		return nil, &keeperError{Code: "invalid_response"}
	}
	items := make([]keeperAuthName, 0, len(rows))
	byIndex := make(map[string]keeperAuthName, len(rows))
	byID := make(map[string]string, len(rows))
	for _, row := range rows {
		if row.AuthType == nil || row.Type == nil || row.Provider == nil || row.Deleted == nil {
			return nil, &keeperError{Code: "invalid_response"}
		}
		if *row.AuthType != 1 || *row.Type != "codex" || *row.Provider != "codex" || *row.Deleted {
			continue
		}
		if row.ID == nil || row.Identity == nil || strings.TrimSpace(*row.Identity) == "" || row.DisplayName == nil || len(row.Alias) == 0 {
			return nil, &keeperError{Code: "invalid_response"}
		}
		id, err := strconv.ParseInt(*row.ID, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != *row.ID {
			return nil, &keeperError{Code: "invalid_response"}
		}
		var alias string
		if err := json.Unmarshal(row.Alias, &alias); err != nil {
			return nil, &keeperError{Code: "invalid_response"}
		}
		item := keeperAuthName{IdentityID: *row.ID, AuthIndex: *row.Identity, Alias: alias, DisplayName: *row.DisplayName}
		if previous, ok := byIndex[item.AuthIndex]; ok {
			if previous != item {
				return nil, &keeperError{Code: "invalid_response"}
			}
			continue
		}
		if previous, ok := byID[item.IdentityID]; ok && previous != item.AuthIndex {
			return nil, &keeperError{Code: "invalid_response"}
		}
		byIndex[item.AuthIndex] = item
		byID[item.IdentityID] = item.AuthIndex
		items = append(items, item)
	}
	return items, nil
}

type keeperAuthNameService struct {
	mu          sync.Mutex
	client      *keeperClient
	configError error
	now         func() time.Time
	cached      keeperAuthNamesResponse
	validUntil  time.Time
	retryUntil  time.Time
	flight      *keeperNamesFlight
	generation  uint64
}

type keeperNamesFlight struct {
	done   chan struct{}
	result keeperAuthNamesResponse
}

func cloneKeeperNamesResponse(r keeperAuthNamesResponse) keeperAuthNamesResponse {
	r.Items = append([]keeperAuthName{}, r.Items...)
	return r
}

func newKeeperAuthNameService(client *keeperClient, err error) *keeperAuthNameService {
	return &keeperAuthNameService{client: client, configError: err, now: time.Now}
}

func (s *keeperAuthNameService) get(ctx context.Context, force bool) keeperAuthNamesResponse {
	if s.configError != nil {
		return keeperNamesUnavailable("configuration_error")
	}
	s.mu.Lock()
	now := s.now()
	if now.Before(s.retryUntil) {
		result := cloneKeeperNamesResponse(s.cached)
		result.RetryAfterSeconds = int64(math.Ceil(s.retryUntil.Sub(now).Seconds()))
		s.mu.Unlock()
		return result
	}
	if !force && s.cached.Status == "ready" && now.Before(s.validUntil) {
		result := cloneKeeperNamesResponse(s.cached)
		s.mu.Unlock()
		return result
	}
	if flight := s.flight; flight != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return keeperNamesUnavailable("timeout")
		case <-flight.done:
			return cloneKeeperNamesResponse(flight.result)
		}
	}
	flight := &keeperNamesFlight{done: make(chan struct{})}
	s.flight = flight
	generation := s.generation
	s.mu.Unlock()
	// HTTP and login run without the service, configuration or state mutexes.
	items, err := s.client.fetchAuthNames(ctx)
	s.mu.Lock()
	if generation != s.generation {
		flight.result = cloneKeeperNamesResponse(s.cached)
		s.flight = nil
		close(flight.done)
		s.mu.Unlock()
		return cloneKeeperNamesResponse(flight.result)
	}
	now = s.now()
	result := keeperAuthNamesResponse{Status: "ready", Items: items, FetchedAt: now.UTC().Format(time.RFC3339Nano)}
	s.validUntil = time.Time{}
	s.retryUntil = time.Time{}
	if err != nil {
		var ke *keeperError
		if !errors.As(err, &ke) {
			ke = &keeperError{Code: "connection_failed"}
		}
		result = keeperNamesUnavailable(ke.Code)
		cooldown := time.Duration(0)
		if ke.Code == "authentication_failed" {
			cooldown = 60 * time.Second
		}
		if ke.Code == "rate_limited" {
			cooldown = ke.RetryAfter
		}
		if cooldown > 0 {
			s.retryUntil = now.Add(cooldown)
			result.RetryAfterSeconds = int64(math.Ceil(cooldown.Seconds()))
		}
	} else {
		s.validUntil = now.Add(60 * time.Second)
	}
	s.cached = result
	flight.result = result
	s.flight = nil
	close(flight.done)
	s.mu.Unlock()
	return cloneKeeperNamesResponse(result)
}

var keeperNameServices struct {
	sync.Mutex
	url, passwordEnv string
	service          *keeperAuthNameService
}

func keeperAuthNamesForConfig(cfg Config, force bool) keeperAuthNamesResponse {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	service, code := keeperNameServiceForConfig(cfg)
	if code != "" {
		return keeperNamesUnavailable(code)
	}
	if service == nil {
		return keeperAuthNamesResponse{Status: "disabled", Items: []keeperAuthName{}}
	}
	result := service.get(ctx, force)
	keeperNameServices.Lock()
	current := keeperNameServices.service == service
	keeperNameServices.Unlock()
	if !current {
		return keeperNamesUnavailable("configuration_error")
	}
	return result
}

func keeperNameServiceForConfig(cfg Config) (*keeperAuthNameService, string) {
	baseURL := strings.TrimSpace(cfg.UsageKeeperURL)
	passwordEnv := strings.TrimSpace(cfg.UsageKeeperPasswordEnv)
	// Service selection shares the configuration read lock so a stale snapshot
	// cannot reinstall an obsolete client after lifecycle reset.
	loadedConfigMu.RLock()
	if cfg.UsageKeeperURL != loadedCfg.UsageKeeperURL || cfg.UsageKeeperPasswordEnv != loadedCfg.UsageKeeperPasswordEnv {
		loadedConfigMu.RUnlock()
		return nil, "configuration_error"
	}
	keeperNameServices.Lock()
	loadedConfigMu.RUnlock()
	if baseURL == "" {
		keeperNameServices.service = nil
		keeperNameServices.Unlock()
		return nil, ""
	}
	if keeperNameServices.service == nil || keeperNameServices.url != baseURL || keeperNameServices.passwordEnv != passwordEnv {
		client, err := newKeeperClient(baseURL, passwordEnv)
		keeperNameServices.url = baseURL
		keeperNameServices.passwordEnv = passwordEnv
		keeperNameServices.service = newKeeperAuthNameService(client, err)
	}
	service := keeperNameServices.service
	keeperNameServices.Unlock()
	return service, ""
}

// PATCH must retain the authentication phase: a rejected first request followed
// by failed login is known not to have changed the identity. Transport failures
// during either actual PATCH remain unknown and are never retried.
func (c *keeperClient) patchAuthName(ctx context.Context, id string, payload []byte) (*http.Response, string, error) {
	path := "/api/v1/usage/identities/" + id
	response, err := c.request(ctx, http.MethodPatch, path, payload)
	if err != nil {
		return nil, "unknown", err
	}
	if response.StatusCode != http.StatusUnauthorized {
		return response, "", nil
	}
	_ = response.Body.Close()
	password := os.Getenv(c.passwordEnv)
	if password == "" {
		return nil, "unavailable", &keeperError{Code: "configuration_error"}
	}
	loginBody, _ := json.Marshal(map[string]string{"password": password})
	login, err := c.request(ctx, http.MethodPost, "/api/v1/auth/login", loginBody)
	if err != nil {
		return nil, "unavailable", err
	}
	_ = login.Body.Close()
	if login.StatusCode != http.StatusOK && login.StatusCode != http.StatusNoContent {
		return nil, "unavailable", keeperHTTPError(login)
	}
	response, err = c.request(ctx, http.MethodPatch, path, payload)
	if err != nil {
		return nil, "unknown", err
	}
	return response, "", nil
}

func (s *keeperAuthNameService) publishUpdate(items []keeperAuthName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.validUntil = time.Time{}
	s.retryUntil = time.Time{}
	s.cached = keeperNamesUnavailable("refresh_required")
	if items != nil {
		s.cached = keeperAuthNamesResponse{Status: "ready", Items: items, FetchedAt: s.now().UTC().Format(time.RFC3339Nano)}
		s.validUntil = s.now().Add(60 * time.Second)
	}
}
