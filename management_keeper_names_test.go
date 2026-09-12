package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// Keep the upstream fixture shaped like Keeper's response. Sensitive metadata
// is intentional: the management projection must never forward these fields.
func keeperIdentityFixture(id, index string, alias any, display string) map[string]any {
	return map[string]any{
		"id": id, "identity": index, "name": "same-file.json", "alias": alias,
		"displayName": display, "auth_type": 1, "auth_type_name": "oauth",
		"type": "codex", "provider": "codex", "prefix": "", "disabled": false,
		"file_name": "same-file.json", "file_path": "/private/credential.json",
		"priority": 0, "note": "private metadata", "is_deleted": false,
		"total_requests": 0, "success_count": 0, "failure_count": 0,
		"input_tokens": 0, "output_tokens": 0, "reasoning_tokens": 0,
		"cache_read_tokens": 0, "total_tokens": 0, "last_aggregated_usage_event_id": "0",
		"period_stats": map[string]int{}, "created_at": "2026-09-12T00:00:00Z", "updated_at": "2026-09-12T00:00:00Z",
	}
}

func TestManagementKeeperNamesS1ReadsIdentity(t *testing.T) {
	rows := []map[string]any{
		keeperIdentityFixture("17", "idx-a", "生产", " 生产 "),
		keeperIdentityFixture("18", "idx-b", nil, "same-file.json"),
		keeperIdentityFixture("19", "idx-c", "", "default"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/usage/identities" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"identities": rows})
	}))
	defer server.Close()
	setupManagementTest(t, Config{Enabled: true, UsageKeeperURL: server.URL})
	got := managementKeeperAuthNames(false)
	var body keeperAuthNamesResponse
	decodeBody(t, got, &body)
	want := []keeperAuthName{{"17", "idx-a", "生产", " 生产 "}, {"18", "idx-b", "", "same-file.json"}, {"19", "idx-c", "", "default"}}
	if got.StatusCode != 200 || got.Headers.Get("Cache-Control") != "no-store" || body.Status != "ready" || !reflect.DeepEqual(body.Items, want) || body.FetchedAt == "" {
		t.Fatalf("unexpected response: %s", got.Body)
	}
	for _, secret := range []string{"file_path", "/private/", "private metadata", "auth_type"} {
		if strings.Contains(string(got.Body), secret) {
			t.Fatalf("projection leaked %s", secret)
		}
	}
}

func TestManagementKeeperNamesS2Validation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		alter      func([]map[string]any) any
		wantStatus string
		count      int
	}{
		{"empty", func(_ []map[string]any) any { return []any{} }, "ready", 0},
		{"filter", func(rows []map[string]any) any {
			rows[1]["auth_type"] = 2
			rows[2]["type"] = "claude"
			rows[3]["is_deleted"] = true
			return rows
		}, "ready", 1},
		{"duplicate index", func(rows []map[string]any) any { rows[1]["identity"] = "idx-a"; return rows }, "unavailable", 0},
		{"duplicate id", func(rows []map[string]any) any { rows[1]["id"] = "17"; return rows }, "unavailable", 0},
		{"identical duplicate", func(rows []map[string]any) any { return []any{rows[0], rows[0]} }, "ready", 1},
		{"missing id", func(rows []map[string]any) any { delete(rows[0], "id"); return rows }, "unavailable", 0},
		{"invalid id", func(rows []map[string]any) any { rows[0]["id"] = "../17"; return rows }, "unavailable", 0},
		{"missing index", func(rows []map[string]any) any { delete(rows[0], "identity"); return rows }, "unavailable", 0},
		{"empty index", func(rows []map[string]any) any { rows[0]["identity"] = ""; return rows }, "unavailable", 0},
		{"missing alias", func(rows []map[string]any) any { delete(rows[0], "alias"); return rows }, "unavailable", 0},
		{"missing display", func(rows []map[string]any) any { delete(rows[0], "displayName"); return rows }, "unavailable", 0},
		{"missing deleted", func(rows []map[string]any) any { delete(rows[0], "is_deleted"); return rows }, "unavailable", 0},
		{"missing auth type", func(rows []map[string]any) any { delete(rows[0], "auth_type"); return rows }, "unavailable", 0},
		{"missing type", func(rows []map[string]any) any { delete(rows[0], "type"); return rows }, "unavailable", 0},
		{"wrong alias", func(rows []map[string]any) any { rows[0]["alias"] = 42; return rows }, "unavailable", 0},
		{"null list", func(_ []map[string]any) any { return nil }, "unavailable", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := []map[string]any{keeperIdentityFixture("17", "idx-a", "A", "A"), keeperIdentityFixture("18", "idx-b", "B", "B"), keeperIdentityFixture("19", "idx-c", "C", "C"), keeperIdentityFixture("20", "idx-d", "D", "D")}
			payload := tc.alter(rows)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"identities": payload})
			}))
			defer server.Close()
			setupManagementTest(t, Config{UsageKeeperURL: server.URL})
			got := managementKeeperAuthNames(false)
			var body keeperAuthNamesResponse
			decodeBody(t, got, &body)
			if got.StatusCode != 200 || body.Status != tc.wantStatus || body.Items == nil || len(body.Items) != tc.count || (tc.wantStatus == "unavailable" && body.ErrorCode != "invalid_response") {
				t.Fatalf("response=%s", got.Body)
			}
		})
	}
}

func TestManagementKeeperNamesS2Failures(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{"missing list", `{}`, "invalid_response", 200},
		{"html", `<html>upstream-private-secret</html>`, "invalid_response", 200},
		{"trailing json", `{"identities":[]} {}`, "invalid_response", 200},
		{"oversized", strings.Repeat(" ", 8<<20) + `{"identities":[]}`, "invalid_response", 200},
		{"forbidden", "upstream-private-secret", "authentication_failed", 403},
		{"server failure", "upstream-private-secret", "connection_failed", 500},
		{"rate limited", "upstream-private-secret", "rate_limited", 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			setupManagementTest(t, Config{UsageKeeperURL: server.URL})
			got := managementKeeperAuthNames(true)
			var body keeperAuthNamesResponse
			decodeBody(t, got, &body)
			if got.StatusCode != 200 || body.Status != "unavailable" || body.ErrorCode != tc.code || len(body.Items) != 0 || strings.Contains(string(got.Body), "upstream-private-secret") {
				t.Fatalf("response=%s", got.Body)
			}
		})
	}
	for _, tc := range []struct{ url, status, code string }{{"", "disabled", ""}, {"file:///private/credentials", "unavailable", "configuration_error"}} {
		setupManagementTest(t, Config{UsageKeeperURL: tc.url})
		got := managementKeeperAuthNames(false)
		var body keeperAuthNamesResponse
		decodeBody(t, got, &body)
		if got.StatusCode != 200 || body.Status != tc.status || body.ErrorCode != tc.code || body.Items == nil {
			t.Fatalf("response=%s", got.Body)
		}
	}
}

func TestManagementKeeperNamesS4LoginAndSession(t *testing.T) {
	t.Setenv("KEEPER_NAMES_TEST_PASSWORD", "names secret")
	var reads, logins atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			logins.Add(1)
			var body struct {
				Password string `json:"password"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password != "names secret" || r.Method != "POST" || r.Header.Get("X-CPA-Usage-Keeper-Request") != "fetch" {
				t.Error("bad login")
			}
			http.SetCookie(w, &http.Cookie{Name: "keeper_session", Value: "names-session", Path: "/"})
			w.WriteHeader(204)
			return
		}
		reads.Add(1)
		cookie, err := r.Cookie("keeper_session")
		if err != nil || cookie.Value != "names-session" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"identities": []any{keeperIdentityFixture("17", "idx-a", "A", "A")}})
	}))
	defer server.Close()
	setupManagementTest(t, Config{UsageKeeperURL: server.URL, UsageKeeperPasswordEnv: "KEEPER_NAMES_TEST_PASSWORD"})
	for range 2 {
		var body keeperAuthNamesResponse
		decodeBody(t, managementKeeperAuthNames(true), &body)
		if body.Status != "ready" {
			t.Fatal(body)
		}
	}
	if reads.Load() != 3 || logins.Load() != 1 {
		t.Fatalf("reads=%d logins=%d", reads.Load(), logins.Load())
	}
}
