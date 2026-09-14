// notification_members.go serves the member-picker: it wraps the platform
// directory clients in a cached, single-flight service (the keeper-aliases
// discipline) and exposes POST /notifications/fetch-members. The endpoint is
// a read over request-body credentials — it never mutates state, never joins
// the audit gate and never occupies the management mutation mutex.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type notificationMembersResponse struct {
	Status            string               `json:"status"`
	Members           []notificationMember `json:"members"`
	FetchedAt         string               `json:"fetched_at,omitempty"`
	ErrorCode         string               `json:"error_code,omitempty"`
	RetryAfterSeconds int64                `json:"retry_after_seconds,omitempty"`
}

func membersUnavailable(code string) notificationMembersResponse {
	return notificationMembersResponse{Status: "unavailable", Members: []notificationMember{}, ErrorCode: code}
}

func cloneMembersResponse(r notificationMembersResponse) notificationMembersResponse {
	r.Members = append([]notificationMember{}, r.Members...)
	return r
}

// notificationMemberCredentials mirrors the fetch_* fields of
// PlatformIdentity (all optional; each platform needs its own pair).
type notificationMemberCredentials struct {
	FetchAppID     string `json:"fetch_app_id"`
	FetchAppSecret string `json:"fetch_app_secret"`
	FetchAppKey    string `json:"fetch_app_key"`
	FetchCorpID    string `json:"fetch_corp_id"`
	FetchSecret    string `json:"fetch_secret"`
}

func (c notificationMemberCredentials) trimmed() notificationMemberCredentials {
	c.FetchAppID = strings.TrimSpace(c.FetchAppID)
	c.FetchAppSecret = strings.TrimSpace(c.FetchAppSecret)
	c.FetchAppKey = strings.TrimSpace(c.FetchAppKey)
	c.FetchCorpID = strings.TrimSpace(c.FetchCorpID)
	c.FetchSecret = strings.TrimSpace(c.FetchSecret)
	return c
}

// missingMemberCredentials reports whether the platform's credential pair is
// incomplete. Closed Chinese errors only — never echo the values back.
func missingMemberCredentials(kind PlatformKind, c notificationMemberCredentials) string {
	c = c.trimmed()
	switch kind {
	case PlatformFeishu:
		if c.FetchAppID == "" || c.FetchAppSecret == "" {
			return "飞书拉取凭证不完整：需要 App ID 与 App Secret"
		}
	case PlatformDingTalk:
		if c.FetchAppKey == "" || c.FetchAppSecret == "" {
			return "钉钉拉取凭证不完整：需要 AppKey 与 AppSecret"
		}
	case PlatformWeCom:
		if c.FetchCorpID == "" || c.FetchSecret == "" {
			return "企业微信拉取凭证不完整：需要 Corp ID 与 Secret"
		}
	}
	return ""
}

// newMemberFetchFunc binds a platform directory client to the credentials.
func newMemberFetchFunc(kind PlatformKind, c notificationMemberCredentials) (func(context.Context) ([]notificationMember, error), error) {
	c = c.trimmed()
	if msg := missingMemberCredentials(kind, c); msg != "" {
		return nil, &keeperError{Code: "configuration_error"}
	}
	switch kind {
	case PlatformFeishu:
		return func(ctx context.Context) ([]notificationMember, error) {
			return fetchFeishuMembers(ctx, c.FetchAppID, c.FetchAppSecret)
		}, nil
	case PlatformDingTalk:
		return func(ctx context.Context) ([]notificationMember, error) {
			return fetchDingtalkMembers(ctx, c.FetchAppKey, c.FetchAppSecret)
		}, nil
	case PlatformWeCom:
		return func(ctx context.Context) ([]notificationMember, error) {
			return fetchWecomMembers(ctx, c.FetchCorpID, c.FetchSecret)
		}, nil
	}
	return nil, &keeperError{Code: "configuration_error"}
}

type memberFlight struct {
	done   chan struct{}
	result notificationMembersResponse
}

type memberFetchService struct {
	mu          sync.Mutex
	fetch       func(ctx context.Context) ([]notificationMember, error)
	configError error
	now         func() time.Time
	cached      notificationMembersResponse
	validUntil  time.Time
	retryUntil  time.Time
	flight      *memberFlight
}

func newMemberFetchService(fetch func(context.Context) ([]notificationMember, error), err error) *memberFetchService {
	return &memberFetchService{fetch: fetch, configError: err, now: time.Now}
}

func (s *memberFetchService) get(ctx context.Context, force bool) notificationMembersResponse {
	if s.configError != nil {
		return membersUnavailable("configuration_error")
	}
	s.mu.Lock()
	now := s.now()
	if now.Before(s.retryUntil) {
		result := cloneMembersResponse(s.cached)
		result.RetryAfterSeconds = int64(math.Ceil(s.retryUntil.Sub(now).Seconds()))
		s.mu.Unlock()
		return result
	}
	if !force && s.cached.Status == "ready" && now.Before(s.validUntil) {
		result := cloneMembersResponse(s.cached)
		s.mu.Unlock()
		return result
	}
	if flight := s.flight; flight != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return membersUnavailable("timeout")
		case <-flight.done:
			return cloneMembersResponse(flight.result)
		}
	}
	flight := &memberFlight{done: make(chan struct{})}
	s.flight = flight
	s.mu.Unlock()
	// Neither this mutex nor the plugin configuration/state mutexes guard IO.
	members, err := s.fetch(ctx)
	s.mu.Lock()
	now = s.now()
	result := notificationMembersResponse{Status: "ready", Members: members, FetchedAt: now.UTC().Format(time.RFC3339Nano)}
	s.validUntil = time.Time{}
	s.retryUntil = time.Time{}
	if err != nil {
		var ke *keeperError
		if !errors.As(err, &ke) {
			ke = &keeperError{Code: "connection_failed"}
		}
		result = membersUnavailable(ke.Code)
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
	return cloneMembersResponse(result)
}

// The registry is keyed by platform + credential content, so changed
// credentials never read a stale cache. 32 entries is far beyond the picker
// use case; overflow simply clears the map.
var notificationMemberServices struct {
	sync.Mutex
	services map[string]*memberFetchService
}

func resetNotificationMemberServicesForTest() {
	notificationMemberServices.Lock()
	notificationMemberServices.services = nil
	notificationMemberServices.Unlock()
}

func memberServiceKey(kind PlatformKind, c notificationMemberCredentials) string {
	c = c.trimmed()
	sum := sha256.Sum256([]byte(string(kind) + "\x00" + c.FetchAppID + "\x00" + c.FetchAppSecret + "\x00" + c.FetchAppKey + "\x00" + c.FetchCorpID + "\x00" + c.FetchSecret))
	return hex.EncodeToString(sum[:])
}

// fetchNotificationMembers resolves (or creates) the service for these
// credentials and fetches with a bounded overall deadline: directory
// traversal across departments and pages must always answer the management
// request in bounded time.
func fetchNotificationMembers(kind PlatformKind, c notificationMemberCredentials, force bool) notificationMembersResponse {
	if !validPlatformKinds[kind] {
		return membersUnavailable("configuration_error")
	}
	key := memberServiceKey(kind, c)
	notificationMemberServices.Lock()
	if notificationMemberServices.services == nil {
		notificationMemberServices.services = map[string]*memberFetchService{}
	} else if len(notificationMemberServices.services) > 32 {
		notificationMemberServices.services = map[string]*memberFetchService{}
	}
	service, ok := notificationMemberServices.services[key]
	if !ok {
		fetch, err := newMemberFetchFunc(kind, c)
		service = newMemberFetchService(fetch, err)
		notificationMemberServices.services[key] = service
	}
	notificationMemberServices.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return service.get(ctx, force)
}

// managementNotificationFetchMembers handles POST /notifications/fetch-members
// with request-body credentials: unsaved credentials must work too, and the
// failure envelope stays HTTP 200 so upstream auth failures cannot clear the
// CPA management session (the keeper-endpoint discipline).
func managementNotificationFetchMembers(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	var body struct {
		Platform    string                        `json:"platform"`
		Credentials notificationMemberCredentials `json:"credentials"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return managementError(http.StatusBadRequest, "请求体格式错误")
	}
	kind := PlatformKind(strings.TrimSpace(body.Platform))
	if !validPlatformKinds[kind] {
		return managementError(http.StatusBadRequest, "未知的通知平台")
	}
	if msg := missingMemberCredentials(kind, body.Credentials); msg != "" {
		return managementError(http.StatusBadRequest, msg)
	}
	response := managementJSON(http.StatusOK, fetchNotificationMembers(kind, body.Credentials, false))
	response.Headers.Set("Cache-Control", "no-store")
	return response
}
