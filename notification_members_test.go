// notification_members_test.go covers the member-fetch service layer (cache,
// single-flight, cooldown, registry) and the fetch-members management
// endpoint (envelope, 400s, no audit side effects).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// auditRawJSONL concatenates today's audit journal beside the state file; an
// absent journal reads as empty — the endpoint under test writes no audit.
func auditRawJSONL(t *testing.T, statePath string) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(filepath.Dir(statePath), "model-mapper-plus-audit", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		return ""
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func newTestMemberService(fetch func(context.Context) ([]notificationMember, error)) *memberFetchService {
	service := newMemberFetchService(fetch, nil)
	base := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return base }
	return service
}

func TestMemberFetchServiceCachesFor60s(t *testing.T) {
	var calls int
	service := newTestMemberService(func(context.Context) ([]notificationMember, error) {
		calls++
		return []notificationMember{{ID: "ou_a", Name: "A"}}, nil
	})
	clock := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }

	first := service.get(context.Background(), false)
	if first.Status != "ready" || len(first.Members) != 1 || first.FetchedAt == "" {
		t.Fatalf("first=%+v", first)
	}
	if again := service.get(context.Background(), false); again.Status != "ready" || calls != 1 {
		t.Fatalf("cache miss after fetch, calls=%d resp=%+v", calls, again)
	}
	clock = clock.Add(59 * time.Second)
	service.get(context.Background(), false)
	if calls != 1 {
		t.Fatalf("cache expired early, calls=%d", calls)
	}
	clock = clock.Add(2 * time.Second)
	service.get(context.Background(), false)
	if calls != 2 {
		t.Fatalf("cache never expired, calls=%d", calls)
	}
	forced := service.get(context.Background(), true)
	if calls != 3 || forced.Status != "ready" {
		t.Fatalf("force must bypass cache, calls=%d resp=%+v", calls, forced)
	}
}

func TestMemberFetchServiceClonesOnReturn(t *testing.T) {
	service := newTestMemberService(func(context.Context) ([]notificationMember, error) {
		return []notificationMember{{ID: "ou_a", Name: "A"}}, nil
	})
	got := service.get(context.Background(), false)
	got.Members[0].Name = "mutated"
	again := service.get(context.Background(), false)
	if again.Members[0].Name != "A" {
		t.Fatalf("caller mutation leaked into cache: %+v", again.Members)
	}
}

func TestMemberFetchServiceCoalescesAndWaiterTimeout(t *testing.T) {
	release := make(chan struct{})
	var calls int
	service := newTestMemberService(func(context.Context) ([]notificationMember, error) {
		calls++
		<-release
		return []notificationMember{{ID: "ou_a", Name: "A"}}, nil
	})
	leader := make(chan notificationMembersResponse, 1)
	go func() { leader <- service.get(context.Background(), false) }()

	// A waiter joining the in-flight fetch shares its result once done.
	waiterDone := make(chan notificationMembersResponse, 1)
	go func() { waiterDone <- service.get(context.Background(), false) }()
	time.Sleep(50 * time.Millisecond) // let the waiter park on the flight
	close(release)
	if resp := <-leader; resp.Status != "ready" {
		t.Fatalf("leader=%+v", resp)
	}
	if resp := <-waiterDone; resp.Status != "ready" || calls != 1 {
		t.Fatalf("waiter=%+v calls=%d", resp, calls)
	}

	// A cancelled waiter gets a timeout envelope without a second fetch.
	blocked := make(chan struct{})
	service2 := newTestMemberService(func(context.Context) ([]notificationMember, error) {
		<-blocked
		return nil, nil
	})
	go func() { service2.get(context.Background(), false) }()
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if resp := service2.get(ctx, false); resp.Status != "unavailable" || resp.ErrorCode != "timeout" {
		t.Fatalf("cancelled waiter=%+v", resp)
	}
	close(blocked)
}

func TestMemberFetchServiceFailureCooldown(t *testing.T) {
	var fail bool
	service := newTestMemberService(func(context.Context) ([]notificationMember, error) {
		if fail {
			return nil, &keeperError{Code: "authentication_failed"}
		}
		return []notificationMember{{ID: "u1", Name: "N"}}, nil
	})
	clock := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }

	fail = true
	first := service.get(context.Background(), false)
	if first.Status != "unavailable" || first.ErrorCode != "authentication_failed" || first.RetryAfterSeconds != 60 {
		t.Fatalf("first=%+v", first)
	}
	// Cooldown swallows even forced refreshes and reports the remaining window.
	forced := service.get(context.Background(), true)
	if forced.ErrorCode != "authentication_failed" || forced.RetryAfterSeconds <= 0 {
		t.Fatalf("cooldown bypassed: %+v", forced)
	}
	clock = clock.Add(30 * time.Second)
	if resp := service.get(context.Background(), false); resp.RetryAfterSeconds != 30 {
		t.Fatalf("cooldown window=%+v", resp)
	}
	clock = clock.Add(31 * time.Second)
	fail = false
	if resp := service.get(context.Background(), false); resp.Status != "ready" {
		t.Fatalf("cooldown must release after expiry: %+v", resp)
	}
}

func TestMemberFetchServiceRateLimitedUsesRetryAfter(t *testing.T) {
	service := newTestMemberService(func(context.Context) ([]notificationMember, error) {
		return nil, &keeperError{Code: "rate_limited", RetryAfter: 120 * time.Second}
	})
	resp := service.get(context.Background(), false)
	if resp.ErrorCode != "rate_limited" || resp.RetryAfterSeconds != 120 {
		t.Fatalf("resp=%+v", resp)
	}
}

func TestMemberFetchServiceUnknownErrorFallsBack(t *testing.T) {
	service := newTestMemberService(func(context.Context) ([]notificationMember, error) {
		return nil, errors.New("weird upstream failure")
	})
	resp := service.get(context.Background(), false)
	if resp.Status != "unavailable" || resp.ErrorCode != "connection_failed" {
		t.Fatalf("resp=%+v", resp)
	}
}

func TestMemberServiceRegistryReusesByCredentials(t *testing.T) {
	resetNotificationMemberServicesForTest()
	t.Cleanup(resetNotificationMemberServicesForTest)
	creds := notificationMemberCredentials{FetchAppID: "cli_x", FetchAppSecret: "s"}

	key := memberServiceKey(PlatformFeishu, creds)
	if key != memberServiceKey(PlatformFeishu, notificationMemberCredentials{FetchAppID: "cli_x", FetchAppSecret: "s"}) {
		t.Fatal("same credentials must hash to the same key")
	}
	if key == memberServiceKey(PlatformDingTalk, creds) {
		t.Fatal("platform must be part of the key")
	}
	if key == memberServiceKey(PlatformFeishu, notificationMemberCredentials{FetchAppID: "cli_x", FetchAppSecret: "other"}) {
		t.Fatal("secret must be part of the key")
	}

	first := fetchNotificationMembers(PlatformFeishu, creds, false)
	if first.Status != "unavailable" || first.ErrorCode != "authentication_failed" {
		// No server: token call fails with connection/auth at worst; what
		// matters is that a service got created and cached.
		t.Fatalf("first=%+v", first)
	}
	notificationMemberServices.Lock()
	cached := len(notificationMemberServices.services)
	notificationMemberServices.Unlock()
	if cached != 1 {
		t.Fatalf("registry size=%d", cached)
	}
	// Same credentials reuse the cached service (no new entry).
	fetchNotificationMembers(PlatformFeishu, creds, false)
	notificationMemberServices.Lock()
	cached = len(notificationMemberServices.services)
	notificationMemberServices.Unlock()
	if cached != 1 {
		t.Fatalf("registry grew on same credentials: %d", cached)
	}
}

func TestFetchMembersEndpointRejectsBadRequests(t *testing.T) {
	withTempNotificationState(t)
	for _, tc := range []struct{ name, body string }{
		{"unknown platform", `{"platform":"telegram","credentials":{"fetch_app_id":"x","fetch_app_secret":"y"}}`},
		{"missing feishu secret", `{"platform":"feishu","credentials":{"fetch_app_id":"x"}}`},
		{"missing dingtalk key", `{"platform":"dingtalk","credentials":{"fetch_app_secret":"y"}}`},
		{"missing wecom corp id", `{"platform":"wecom","credentials":{"fetch_secret":"y"}}`},
		{"malformed body", `not-json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := dispatchManagement(mgmtRequest(http.MethodPost, "/v0/management/plugins/model-mapper-plus/notifications/fetch-members", tc.body))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
			}
			if strings.Contains(string(resp.Body), "app-sec") || strings.Contains(string(resp.Body), "cli_") {
				t.Fatal("400 body must not echo credentials")
			}
		})
	}
}

func TestFetchMembersEndpointReturnsEnvelope(t *testing.T) {
	withTempNotificationState(t)
	resetNotificationMemberServicesForTest()
	t.Cleanup(resetNotificationMemberServicesForTest)
	// Hand the registry a fake fetch via the DingTalk credentials path so the
	// endpoint exercises the envelope without any network IO.
	reset := pointDingtalkMembersAt("http://127.0.0.1:1")
	t.Cleanup(reset)

	resp := dispatchManagement(mgmtRequest(http.MethodPost, "/v0/management/plugins/model-mapper-plus/notifications/fetch-members",
		`{"platform":"dingtalk","credentials":{"fetch_app_key":"k","fetch_app_secret":"s"}}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if cacheControl := resp.Headers.Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("cache-control=%q", cacheControl)
	}
	var out notificationMembersResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "unavailable" || out.ErrorCode == "" || len(out.Members) != 0 {
		t.Fatalf("envelope=%+v", out)
	}
}

func TestFetchMembersEndpointWritesNoAudit(t *testing.T) {
	statePath := setupManagementTest(t, Config{Enabled: true})
	before := auditPageForTest(t).Total
	reset := pointFeishuMembersAt("http://127.0.0.1:1")
	t.Cleanup(reset)
	resetNotificationMemberServicesForTest()
	t.Cleanup(resetNotificationMemberServicesForTest)

	resp := dispatchManagement(auditRequest(http.MethodPost, "/notifications/fetch-members",
		`{"platform":"feishu","credentials":{"fetch_app_id":"cli_secret_x","fetch_app_secret":"app_secret_y"}}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if after := auditPageForTest(t).Total; after != before {
		t.Fatalf("audit total changed: %d -> %d", before, after)
	}
	raw := auditRawJSONL(t, statePath)
	for _, leaked := range []string{"cli_secret_x", "app_secret_y"} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("credential leaked into audit log: %s", leaked)
		}
	}
}

func TestFetchMembersRegistryCap(t *testing.T) {
	resetNotificationMemberServicesForTest()
	t.Cleanup(resetNotificationMemberServicesForTest)
	notificationMemberServices.Lock()
	notificationMemberServices.services = map[string]*memberFetchService{}
	notificationMemberServices.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			creds := notificationMemberCredentials{FetchAppID: "app", FetchAppSecret: strings.Repeat("x", i+1)}
			fetchNotificationMembers(PlatformFeishu, creds, false)
		}(i)
	}
	wg.Wait()
	notificationMemberServices.Lock()
	size := len(notificationMemberServices.services)
	notificationMemberServices.Unlock()
	if size > 32 {
		t.Fatalf("registry size=%d exceeds cap", size)
	}
}
