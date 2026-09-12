package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
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
	got := dispatchManagement(auditRequest(http.MethodGet, "/keeper/auth-names", ""))
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
		{"missing provider", func(rows []map[string]any) any { delete(rows[0], "provider"); return rows }, "unavailable", 0},
		{"wrong provider", func(rows []map[string]any) any { rows[0]["provider"] = "other"; return rows[:1] }, "ready", 0},
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
			got := dispatchManagement(auditRequest(http.MethodGet, "/keeper/auth-names", ""))
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
			got := dispatchManagement(auditRequest(http.MethodPost, "/keeper/auth-names/refresh", ""))
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

func TestManagementKeeperNamesRegistration(t *testing.T) {
	raw, err := handleManagementRegister()
	if err != nil {
		t.Fatal(err)
	}
	var registration pluginapi.ManagementRegistrationResponse
	if err := json.Unmarshal(raw, &registration); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ method, path string }{{"GET", "/keeper/auth-names"}, {"POST", "/keeper/auth-names/refresh"}, {"PATCH", "/keeper/auth-names"}} {
		found := false
		for _, route := range registration.Routes {
			if route.Method == want.method && route.Path == managementRegisterBase+want.path {
				found = true
			}
		}
		if !found {
			t.Errorf("unregistered %s %s", want.method, want.path)
		}
	}
}

func TestManagementKeeperNamesS3ImmediateUpdate(t *testing.T) {
	for _, input := range []string{"新名称", "", strings.Repeat("界", 50), "原名称", "  新名称  ", "👩‍💻"} {
		alias := strings.TrimSpace(input)
		t.Run("alias="+input, func(t *testing.T) {
			var mu sync.Mutex
			saved := "原名称"
			patches := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method == "PATCH" {
					patches++
					if r.URL.Path != "/api/v1/usage/identities/17" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-CPA-Usage-Keeper-Request") != "fetch" {
						t.Errorf("bad PATCH %s %v", r.URL.Path, r.Header)
					}
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || body["alias"] != alias {
						t.Errorf("payload=%v err=%v", body, err)
					}
					saved = body["alias"]
				} else if r.Method != "GET" || r.URL.Path != "/api/v1/usage/identities" {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				display := saved
				if display == "" {
					display = "same-file.json"
				}
				row := keeperIdentityFixture("17", "idx-a", saved, display)
				if r.Method == "GET" {
					_ = json.NewEncoder(w).Encode(map[string]any{"identities": []any{row}})
				} else {
					_ = json.NewEncoder(w).Encode(row)
				}
			}))
			defer server.Close()
			setupManagementTest(t, Config{UsageKeeperURL: server.URL})
			_ = dispatchManagement(auditRequest("GET", "/keeper/auth-names", ""))
			payload, _ := json.Marshal(map[string]string{"auth_index": "idx-a", "alias": input, "identity_id": "999"})
			resp := dispatchManagement(auditRequest("PATCH", "/keeper/auth-names", string(payload)))
			var body struct {
				Status string
				Item   *keeperAuthName
			}
			decodeBody(t, resp, &body)
			if resp.StatusCode != 200 || body.Status != "ready" || body.Item == nil || body.Item.Alias != alias {
				t.Fatalf("response=%s", resp.Body)
			}
			auditMetaForTest(t, resp)
			var read keeperAuthNamesResponse
			decodeBody(t, dispatchManagement(auditRequest("GET", "/keeper/auth-names", "")), &read)
			if len(read.Items) != 1 || read.Items[0] != *body.Item {
				t.Fatalf("cache=%+v update=%+v", read, body)
			}
			decodeBody(t, dispatchManagement(auditRequest("POST", "/keeper/auth-names/refresh", "")), &read)
			if len(read.Items) != 1 || read.Items[0] != *body.Item {
				t.Fatalf("readback=%+v", read)
			}
			if alias == "" && body.Item.DisplayName != "same-file.json" {
				t.Fatal("clear did not restore default name")
			}
			mu.Lock()
			count, stored := patches, saved
			mu.Unlock()
			if count != 1 || stored != alias {
				t.Fatalf("patches=%d saved=%q", count, stored)
			}
			state, _ := loadedStateSnapshot()
			if len(state.KeyBindings) != 0 {
				t.Fatal("name update created local binding")
			}
			page := auditPageForTest(t)
			if page.Total != 1 || page.Items[0].Outcome != "succeeded" || page.Items[0].ObjectType != "keeper_auth_name" || page.Items[0].Changed == nil || *page.Items[0].Changed != (alias != "原名称") {
				t.Fatalf("audit=%+v", page)
			}
		})
	}
}

func TestManagementKeeperNamesS4InvalidInput(t *testing.T) {
	for _, body := range []string{`{`, `{}`, `null`, `{"auth_index":"idx-a"}`, `{"auth_index":"idx-a","alias":null}`, `{"auth_index":"","alias":"A"}`, `{"auth_index":"idx-a","alias":7}`} {
		t.Run(body, func(t *testing.T) { testKeeperInvalidName(t, body) })
	}
	for _, alias := range []string{strings.Repeat("界", 51), "a\nb", "a\x00", "a\u200b", "a\u202e"} {
		body, _ := json.Marshal(map[string]string{"auth_index": "idx-a", "alias": alias})
		t.Run(alias, func(t *testing.T) { testKeeperInvalidName(t, string(body)) })
	}
}

func testKeeperInvalidName(t *testing.T, body string) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	setupManagementTest(t, Config{UsageKeeperURL: server.URL})
	resp := dispatchManagement(auditRequest("PATCH", "/keeper/auth-names", body))
	var result struct{ Status string }
	decodeBody(t, resp, &result)
	if resp.StatusCode != 200 || result.Status != "invalid" || requests.Load() != 0 {
		t.Fatalf("response=%s requests=%d", resp.Body, requests.Load())
	}
	page := auditPageForTest(t)
	if page.Total != 1 || page.Items[0].Outcome != "failed" || page.Items[0].Changed == nil || *page.Items[0].Changed {
		t.Fatalf("audit=%+v", page)
	}
}

func TestManagementKeeperNamesS4FailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		getStatus, patchStatus int
		alter                  func(map[string]any)
		body, status, outcome  string
		patches                int
	}{
		{name: "preflight unavailable", getStatus: 503, status: "unavailable", outcome: "failed"},
		{name: "missing exact index", alter: func(row map[string]any) { row["identity"] = "idx-b" }, status: "not_found", outcome: "failed"},
		{name: "index is not trimmed", alter: func(row map[string]any) { row["identity"] = " idx-a " }, status: "not_found", outcome: "failed"},
		{name: "deleted", alter: func(row map[string]any) { row["is_deleted"] = true }, status: "not_found", outcome: "failed"},
		{name: "wrong provider", alter: func(row map[string]any) { row["provider"] = "other" }, status: "not_found", outcome: "failed"},
		{name: "wrong type", alter: func(row map[string]any) { row["type"] = "claude" }, status: "not_found", outcome: "failed"},
		{name: "patch disappeared", patchStatus: 404, status: "not_found", outcome: "failed", patches: 1},
		{name: "patch invalid", patchStatus: 400, status: "invalid", outcome: "failed", patches: 1},
		{name: "patch forbidden", patchStatus: 403, status: "unavailable", outcome: "failed", patches: 1},
		{name: "patch rate limit", patchStatus: 429, status: "unavailable", outcome: "failed", patches: 1},
		{name: "patch server error", patchStatus: 503, status: "unknown", outcome: "unknown", patches: 1},
		{name: "patch invalid response", patchStatus: 200, body: `{"id":"17","secret":"private-upstream"}`, status: "unknown", outcome: "unknown", patches: 1},
		{name: "patch changed identity", patchStatus: 200, body: `{"id":"18","identity":"idx-a","alias":"new","displayName":"new","auth_type":1,"type":"codex","provider":"codex","is_deleted":false}`, status: "unknown", outcome: "unknown", patches: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var patches atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					if tc.getStatus != 0 {
						w.WriteHeader(tc.getStatus)
						return
					}
					row := keeperIdentityFixture("17", "idx-a", "old", "old")
					if tc.alter != nil {
						tc.alter(row)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"identities": []any{row}})
					return
				}
				patches.Add(1)
				status := tc.patchStatus
				if status == 0 {
					status = 500
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			setupManagementTest(t, Config{UsageKeeperURL: server.URL})
			resp := dispatchManagement(auditRequest("PATCH", "/keeper/auth-names", `{"auth_index":"idx-a","alias":"new"}`))
			var body struct {
				Status string
				Item   *keeperAuthName
			}
			decodeBody(t, resp, &body)
			if resp.StatusCode != 200 || body.Status != tc.status || body.Item != nil || int(patches.Load()) != tc.patches || strings.Contains(string(resp.Body), "private-upstream") {
				t.Fatalf("response=%s patches=%d", resp.Body, patches.Load())
			}
			page := auditPageForTest(t)
			if page.Total != 1 || page.Items[0].Outcome != tc.outcome {
				t.Fatalf("audit=%+v", page)
			}
			if tc.outcome == "unknown" {
				if page.Items[0].Changed != nil {
					t.Fatal("unknown changed must be null")
				}
			} else if page.Items[0].Changed == nil || *page.Items[0].Changed {
				t.Fatal("failed changed must be false")
			}
		})
	}
}

func TestManagementKeeperNamesS4PatchLogin(t *testing.T) {
	for _, mode := range []string{"success", "login denied", "login disconnected", "second unauthorized"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("T04_PASSWORD", "keeper-secret")
			var patches, logins atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				row := keeperIdentityFixture("17", "idx-a", "new", "new")
				if r.Method == "GET" {
					row["alias"] = "old"
					row["displayName"] = "old"
					_ = json.NewEncoder(w).Encode(map[string]any{"identities": []any{row}})
					return
				}
				if r.URL.Path == "/api/v1/auth/login" {
					logins.Add(1)
					var login map[string]string
					if json.NewDecoder(r.Body).Decode(&login) != nil || login["password"] != "keeper-secret" {
						t.Error("bad login payload")
					}
					if mode == "login denied" {
						w.WriteHeader(403)
						return
					}
					if mode == "login disconnected" {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err == nil {
							_ = conn.Close()
						}
						return
					}
					http.SetCookie(w, &http.Cookie{Name: "keeper_session", Value: "private-session", Path: "/"})
					w.WriteHeader(204)
					return
				}
				patches.Add(1)
				if cookie, err := r.Cookie("keeper_session"); err != nil || cookie.Value != "private-session" || mode == "second unauthorized" {
					w.WriteHeader(401)
					return
				}
				_ = json.NewEncoder(w).Encode(row)
			}))
			defer server.Close()
			setupManagementTest(t, Config{UsageKeeperURL: server.URL, UsageKeeperPasswordEnv: "T04_PASSWORD"})
			resp := dispatchManagement(auditRequest("PATCH", "/keeper/auth-names", `{"auth_index":"idx-a","alias":"new"}`))
			var result struct{ Status string }
			decodeBody(t, resp, &result)
			want, count := "unavailable", int32(1)
			if mode == "success" {
				want = "ready"
				count = 2
			}
			if mode == "second unauthorized" {
				count = 2
			}
			if result.Status != want || patches.Load() != count || logins.Load() != 1 {
				t.Fatalf("response=%s patches=%d logins=%d", resp.Body, patches.Load(), logins.Load())
			}
			for _, secret := range []string{"keeper-secret", "private-session"} {
				if strings.Contains(string(resp.Body), secret) {
					t.Fatal("secret leaked")
				}
			}
		})
	}
}

func TestManagementKeeperNamesS8S9AuditFaults(t *testing.T) {
	for _, mode := range []string{"begin-directory", "begin-write", "begin-sync", "finish-write-ready", "finish-sync-ready", "finish-write-unknown", "finish-sync-unknown"} {
		t.Run(mode, func(t *testing.T) {
			var patches atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				row := keeperIdentityFixture("17", "idx-a", "old", "old")
				if r.Method == "GET" {
					_ = json.NewEncoder(w).Encode(map[string]any{"identities": []any{row}})
					return
				}
				patches.Add(1)
				if strings.HasSuffix(mode, "unknown") {
					w.WriteHeader(503)
					return
				}
				row["alias"] = "new"
				row["displayName"] = "new"
				_ = json.NewEncoder(w).Encode(row)
			}))
			defer server.Close()
			statePath := setupManagementTest(t, Config{UsageKeeperURL: server.URL})
			if mode == "begin-directory" {
				if err := os.WriteFile(filepath.Join(filepath.Dir(statePath), "model-mapper-plus-audit"), []byte("blocked"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			original := auditOpenFile
			t.Cleanup(func() { auditOpenFile = original })
			calls := 0
			auditOpenFile = func(path string, flag int, perm os.FileMode) (auditFile, error) {
				file, err := os.OpenFile(path, flag, perm)
				if err != nil {
					return nil, err
				}
				calls++
				fail := calls == 1 && strings.HasPrefix(mode, "begin") || calls == 2 && strings.HasPrefix(mode, "finish")
				return &managementAuditFaultFile{File: file, failWrite: fail && strings.Contains(mode, "write"), failSync: fail && strings.Contains(mode, "sync")}, nil
			}
			resp := dispatchManagement(auditRequest("PATCH", "/keeper/auth-names", `{"auth_index":"idx-a","alias":"new"}`))
			if strings.HasPrefix(mode, "begin") {
				if resp.StatusCode != 503 || patches.Load() != 0 {
					t.Fatalf("response=%s patches=%d", resp.Body, patches.Load())
				}
				return
			}
			var body struct {
				Status string
				Audit  struct {
					Recorded  bool
					ErrorCode string `json:"error_code"`
				}
			}
			decodeBody(t, resp, &body)
			want := "ready"
			if strings.HasSuffix(mode, "unknown") {
				want = "unknown"
			}
			if resp.StatusCode != 200 || body.Status != want || body.Audit.Recorded || body.Audit.ErrorCode != "audit_write_failed" || patches.Load() != 1 {
				t.Fatalf("response=%s patches=%d", resp.Body, patches.Load())
			}
			page := auditPageForTest(t)
			if page.Total != 1 || page.Items[0].Outcome != "unknown" {
				t.Fatalf("audit=%+v", page)
			}
		})
	}
}

func TestManagementKeeperNamesS4OldReadCannotReplaceUpdatedCache(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		row := keeperIdentityFixture("17", "idx-a", "old", "old")
		if r.Method == "GET" {
			if reads.Add(1) == 1 {
				close(entered)
				<-release
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"identities": []any{row}})
			return
		}
		row["alias"] = "new"
		row["displayName"] = "new"
		_ = json.NewEncoder(w).Encode(row)
	}))
	defer server.Close()
	setupManagementTest(t, Config{UsageKeeperURL: server.URL})
	oldRead := make(chan pluginapi.ManagementResponse, 1)
	go func() { oldRead <- dispatchManagement(auditRequest("GET", "/keeper/auth-names", "")) }()
	<-entered
	resp := dispatchManagement(auditRequest("PATCH", "/keeper/auth-names", `{"auth_index":"idx-a","alias":"new"}`))
	if !strings.Contains(string(resp.Body), `"status":"ready"`) {
		once.Do(func() { close(release) })
		t.Fatalf("patch=%s", resp.Body)
	}
	once.Do(func() { close(release) })
	<-oldRead
	var body keeperAuthNamesResponse
	decodeBody(t, dispatchManagement(auditRequest("GET", "/keeper/auth-names", "")), &body)
	if len(body.Items) != 1 || body.Items[0].Alias != "new" {
		t.Fatalf("old flight replaced successful update: %+v", body)
	}
}

func TestManagementKeeperNamesS4OneDeadlineAndSnapshotLocks(t *testing.T) {
	patchEntered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			time.Sleep(time.Second)
			_ = json.NewEncoder(w).Encode(map[string]any{"identities": []any{keeperIdentityFixture("17", "idx-a", "old", "old")}})
			return
		}
		close(patchEntered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	setupManagementTest(t, Config{UsageKeeperURL: server.URL})
	start := time.Now()
	done := make(chan pluginapi.ManagementResponse, 1)
	go func() {
		done <- dispatchManagement(auditRequest("PATCH", "/keeper/auth-names", `{"auth_index":"idx-a","alias":"new"}`))
	}()
	<-patchEntered
	readDone := make(chan struct{})
	go func() { _ = loadedConfig(); _, _ = loadedStateSnapshot(); close(readDone) }()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Error("Keeper network blocked snapshot locks")
	}
	resp := <-done
	close(release)
	if elapsed := time.Since(start); elapsed > 5700*time.Millisecond {
		t.Errorf("deadline restarted: %s", elapsed)
	}
	var body struct{ Status string }
	decodeBody(t, resp, &body)
	if body.Status != "unknown" {
		t.Fatalf("response=%s", resp.Body)
	}
}

func TestManagementKeeperNamesS4PreflightRefreshesIdentity(t *testing.T) {
	for _, mode := range []string{"changed ID", "disappeared", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			var reads, patches atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					rows := []any{keeperIdentityFixture("17", "idx-a", "old", "old")}
					if reads.Add(1) > 1 {
						switch mode {
						case "changed ID":
							rows = []any{keeperIdentityFixture("23", "idx-a", "other old", "other old")}
						case "disappeared":
							rows = []any{}
						case "ambiguous":
							rows = append(rows, keeperIdentityFixture("23", "idx-a", "other", "other"))
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"identities": rows})
					return
				}
				patches.Add(1)
				if r.URL.Path != "/api/v1/usage/identities/23" {
					t.Errorf("used stale ID: %s", r.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(keeperIdentityFixture("23", "idx-a", "new", "new"))
			}))
			defer server.Close()
			setupManagementTest(t, Config{UsageKeeperURL: server.URL})
			_ = dispatchManagement(auditRequest("GET", "/keeper/auth-names", ""))
			resp := dispatchManagement(auditRequest("PATCH", "/keeper/auth-names", `{"auth_index":"idx-a","alias":"new","identity_id":"17"}`))
			var body struct{ Status string }
			decodeBody(t, resp, &body)
			want, count := "ready", int32(1)
			if mode == "disappeared" {
				want = "not_found"
				count = 0
			}
			if mode == "ambiguous" {
				want = "unavailable"
				count = 0
			}
			if body.Status != want || patches.Load() != count || reads.Load() != 2 {
				t.Fatalf("response=%s patches=%d reads=%d", resp.Body, patches.Load(), reads.Load())
			}
			if mode == "changed ID" {
				page := auditPageForTest(t)
				if string(page.Items[0].Changes["alias"].Before) != `"other old"` {
					t.Fatalf("audit used stale preflight: %+v", page)
				}
			}
		})
	}
}

func TestManagementKeeperNamesS4UnknownInvalidatesOldCache(t *testing.T) {
	var patches, reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			patches.Add(1)
			w.WriteHeader(503)
			return
		}
		reads.Add(1)
		alias := "old"
		if patches.Load() > 0 {
			alias = "new"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"identities": []any{keeperIdentityFixture("17", "idx-a", alias, alias)}})
	}))
	defer server.Close()
	setupManagementTest(t, Config{UsageKeeperURL: server.URL})
	_ = dispatchManagement(auditRequest("GET", "/keeper/auth-names", ""))
	resp := dispatchManagement(auditRequest("PATCH", "/keeper/auth-names", `{"auth_index":"idx-a","alias":"new"}`))
	var body struct{ Status string }
	decodeBody(t, resp, &body)
	if body.Status != "unknown" {
		t.Fatalf("response=%s", resp.Body)
	}
	var names keeperAuthNamesResponse
	decodeBody(t, dispatchManagement(auditRequest("GET", "/keeper/auth-names", "")), &names)
	if len(names.Items) != 1 || names.Items[0].Alias != "new" || reads.Load() != 3 || patches.Load() != 1 {
		t.Fatalf("names=%+v reads=%d patches=%d", names, reads.Load(), patches.Load())
	}
	page := auditPageForTest(t)
	if page.Total != 1 || page.Items[0].Outcome != "unknown" {
		t.Fatal("readback must not rewrite operation outcome")
	}
}
