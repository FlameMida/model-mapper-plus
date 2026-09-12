package main

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type keeperAliasesResponse struct {
	Status            string        `json:"status"`
	Items             []keeperAlias `json:"items"`
	FetchedAt         string        `json:"fetched_at,omitempty"`
	ErrorCode         string        `json:"error_code,omitempty"`
	RetryAfterSeconds int64         `json:"retry_after_seconds,omitempty"`
}

func keeperUnavailable(code string) keeperAliasesResponse {
	return keeperAliasesResponse{Status: "unavailable", Items: []keeperAlias{}, ErrorCode: code}
}
func cloneKeeperResponse(r keeperAliasesResponse) keeperAliasesResponse {
	r.Items = append([]keeperAlias{}, r.Items...)
	return r
}

type keeperFlight struct {
	done   chan struct{}
	result keeperAliasesResponse
}
type keeperAliasService struct {
	mu          sync.Mutex
	client      *keeperClient
	configError error
	now         func() time.Time
	cached      keeperAliasesResponse
	validUntil  time.Time
	retryUntil  time.Time
	flight      *keeperFlight
}

func newKeeperAliasService(client *keeperClient, err error) *keeperAliasService {
	return &keeperAliasService{client: client, configError: err, now: time.Now}
}

func (s *keeperAliasService) get(ctx context.Context, force bool) keeperAliasesResponse {
	if s.configError != nil {
		return keeperUnavailable("configuration_error")
	}
	s.mu.Lock()
	now := s.now()
	if now.Before(s.retryUntil) {
		result := cloneKeeperResponse(s.cached)
		result.RetryAfterSeconds = int64(math.Ceil(s.retryUntil.Sub(now).Seconds()))
		s.mu.Unlock()
		return result
	}
	if !force && s.cached.Status == "ready" && now.Before(s.validUntil) {
		result := cloneKeeperResponse(s.cached)
		s.mu.Unlock()
		return result
	}
	if flight := s.flight; flight != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return keeperUnavailable("timeout")
		case <-flight.done:
			return cloneKeeperResponse(flight.result)
		}
	}
	flight := &keeperFlight{done: make(chan struct{})}
	s.flight = flight
	s.mu.Unlock()
	// Neither this mutex nor the plugin configuration/state mutexes guard IO.
	items, err := s.client.fetch(ctx)
	s.mu.Lock()
	now = s.now()
	result := keeperAliasesResponse{Status: "ready", Items: items, FetchedAt: now.UTC().Format(time.RFC3339Nano)}
	s.validUntil = time.Time{}
	s.retryUntil = time.Time{}
	if err != nil {
		var ke *keeperError
		if !errors.As(err, &ke) {
			ke = &keeperError{Code: "connection_failed"}
		}
		result = keeperUnavailable(ke.Code)
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
	return cloneKeeperResponse(result)
}

var keeperServices struct {
	sync.Mutex
	url         string
	passwordEnv string
	service     *keeperAliasService
}

func resetKeeperAliases() {
	keeperServices.Lock()
	keeperServices.service = nil
	keeperServices.Unlock()
	keeperNameServices.Lock()
	keeperNameServices.service = nil
	keeperNameServices.Unlock()
}

func keeperAliasesForConfig(cfg Config, force bool) keeperAliasesResponse {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	baseURL := strings.TrimSpace(cfg.UsageKeeperURL)
	passwordEnv := strings.TrimSpace(cfg.UsageKeeperPasswordEnv)
	// Hold the config read lock through service selection: a snapshot taken
	// before reconfiguration must never reinstall the old client/cache.
	loadedConfigMu.RLock()
	if cfg.UsageKeeperURL != loadedCfg.UsageKeeperURL || cfg.UsageKeeperPasswordEnv != loadedCfg.UsageKeeperPasswordEnv {
		loadedConfigMu.RUnlock()
		return keeperUnavailable("configuration_error")
	}
	keeperServices.Lock()
	loadedConfigMu.RUnlock()
	if baseURL == "" {
		keeperServices.service = nil
		keeperServices.Unlock()
		return keeperAliasesResponse{Status: "disabled", Items: []keeperAlias{}}
	}
	if keeperServices.service == nil || keeperServices.url != baseURL || keeperServices.passwordEnv != passwordEnv {
		client, err := newKeeperClient(baseURL, passwordEnv)
		keeperServices.url = baseURL
		keeperServices.passwordEnv = passwordEnv
		keeperServices.service = newKeeperAliasService(client, err)
	}
	service := keeperServices.service
	keeperServices.Unlock()
	result := service.get(ctx, force)
	keeperServices.Lock()
	current := keeperServices.service == service
	keeperServices.Unlock()
	if !current {
		return keeperUnavailable("configuration_error")
	}
	return result
}

func managementKeeperAliases(force bool) pluginapi.ManagementResponse {
	response := managementJSON(http.StatusOK, keeperAliasesForConfig(loadedConfig(), force))
	response.Headers.Set("Cache-Control", "no-store")
	return response
}
