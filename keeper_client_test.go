package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeeperClientReadsAndReusesSession(t *testing.T) {
	t.Setenv("KEEPER_TEST_PASSWORD", "test password & exact ")
	var logins, reads atomic.Int32
	var expired atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/keeper/api/v1/auth/login":
			logins.Add(1)
			var body struct {
				Password string `json:"password"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password != "test password & exact " || r.Header.Get("X-CPA-Usage-Keeper-Request") != "fetch" || r.Method != "POST" {
				t.Error("invalid login request")
				w.WriteHeader(400)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "keeper_session", Value: "test-session", Path: "/keeper", HttpOnly: true})
			expired.Store(false)
			w.WriteHeader(204)
		case "/keeper/api/v1/usage/api-keys/settings":
			reads.Add(1)
			cookie, err := r.Cookie("keeper_session")
			if err != nil || cookie.Value != "test-session" || expired.Load() {
				w.WriteHeader(401)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"apiKey":"sk-one","keyAlias":"  团队 & <主用>  "},{"apiKey":"sk-two","keyAlias":""}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, err := newKeeperClient(server.URL+"/keeper/", "KEEPER_TEST_PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		rows, err := client.fetch(context.Background())
		if err != nil || len(rows) != 2 || rows[0].Key != "sk-one" || rows[0].Alias != "团队 & <主用>" || rows[1].Alias != "" {
			t.Fatalf("rows=%+v err=%v", rows, err)
		}
	}
	if logins.Load() != 1 || reads.Load() != 3 {
		t.Fatalf("logins=%d reads=%d", logins.Load(), reads.Load())
	}
	expired.Store(true)
	if _, err := client.fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if logins.Load() != 2 || reads.Load() != 5 {
		t.Fatal("session was not renewed exactly once")
	}
}

func TestKeeperClientResponseValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		count            int
	}{
		{"empty", `{"items":[]}`, "", 0},
		{"deduplicate", `{"items":[{"apiKey":"sk-a","keyAlias":"A"},{"apiKey":"sk-a","keyAlias":"A"}]}`, "", 1},
		{"conflicting", `{"items":[{"apiKey":"sk-a","keyAlias":"A"},{"apiKey":"sk-a","keyAlias":"B"}]}`, "invalid_response", 0},
		{"missing items", `{}`, "invalid_response", 0},
		{"null items", `{"items":null}`, "invalid_response", 0},
		{"missing key", `{"items":[{"keyAlias":"A"}]}`, "invalid_response", 0},
		{"missing alias", `{"items":[{"apiKey":"sk-a"}]}`, "invalid_response", 0},
		{"wrong type", `{"items":[{"apiKey":"sk-a","keyAlias":3}]}`, "invalid_response", 0},
		{"empty key", `{"items":[{"apiKey":"","keyAlias":"A"}]}`, "invalid_response", 0},
		{"html", `<html>login</html>`, "invalid_response", 0},
		{"trailing json", `{"items":[]} {}`, "invalid_response", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer s.Close()
			client, err := newKeeperClient(s.URL, "")
			if err != nil {
				t.Fatal(err)
			}
			rows, err := client.fetch(context.Background())
			if tc.code == "" {
				if err != nil || len(rows) != tc.count {
					t.Fatalf("rows=%v err=%v", rows, err)
				}
				return
			}
			requireKeeperError(t, err, tc.code)
		})
	}
}

func requireKeeperError(t *testing.T, err error, code string) *keeperError {
	t.Helper()
	var target *keeperError
	if !errors.As(err, &target) || target.Code != code {
		t.Fatalf("error=%v want %s", err, code)
	}
	return target
}

func TestKeeperClientAuthenticationErrors(t *testing.T) {
	for _, tc := range []struct {
		name, password              string
		loginStatus, settingsStatus int
		code                        string
		wantLogins                  int
	}{
		{"missing password", "", 204, 401, "configuration_error", 0},
		{"wrong password", "test-secret", 401, 401, "authentication_failed", 1},
		{"forbidden", "test-secret", 204, 403, "authentication_failed", 0},
		{"login does not authorize", "test-secret", 204, 401, "authentication_failed", 1},
		{"rate limited", "test-secret", 429, 401, "rate_limited", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KEEPER_TEST_PASSWORD", tc.password)
			var logins atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					logins.Add(1)
					w.Header().Set("Retry-After", "120")
					w.WriteHeader(tc.loginStatus)
				} else {
					w.WriteHeader(tc.settingsStatus)
				}
				_, _ = w.Write([]byte("do-not-leak-test-secret"))
			}))
			defer s.Close()
			client, err := newKeeperClient(s.URL, "KEEPER_TEST_PASSWORD")
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.fetch(context.Background())
			ke := requireKeeperError(t, err, tc.code)
			if err.Error() != tc.code {
				t.Fatal("error leaked upstream data")
			}
			if tc.code == "rate_limited" && ke.RetryAfter != 120*time.Second {
				t.Fatalf("retry=%v", ke.RetryAfter)
			}
			if int(logins.Load()) != tc.wantLogins {
				t.Fatalf("logins=%d", logins.Load())
			}
		})
	}
}

func TestKeeperClientRejectsURLAndRedirect(t *testing.T) {
	for _, raw := range []string{"", "file:///tmp/a", "http://user:password@localhost", "http://localhost?secret=x", "http://localhost#fragment", "/relative"} {
		_, err := newKeeperClient(raw, "")
		requireKeeperError(t, err, "configuration_error")
	}
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	client, err := newKeeperClient(source.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.fetch(context.Background())
	requireKeeperError(t, err, "invalid_response")
	if hits.Load() != 0 {
		t.Fatal("followed redirect")
	}
}

func TestKeeperClientContextDeadline(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	client, err := newKeeperClient(s.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = client.fetch(ctx)
	requireKeeperError(t, err, "timeout")
}

func TestKeeperClientPatchHeadersAndSingleAuthenticationRetry(t *testing.T) {
	t.Setenv("KEEPER_PATCH_TEST_PASSWORD", "secret")
	for _, status := range []int{200, 401, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var patches, logins atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/auth/login" {
					logins.Add(1)
					w.WriteHeader(204)
					return
				}
				count := patches.Add(1)
				if r.Method != "PATCH" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-CPA-Usage-Keeper-Request") != "fetch" {
					t.Error("missing PATCH request headers")
				}
				var body struct {
					Alias string `json:"alias"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Alias != "new name" {
					t.Error("PATCH body changed")
				}
				if count == 1 {
					w.WriteHeader(401)
					return
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			client, err := newKeeperClient(server.URL, "KEEPER_PATCH_TEST_PASSWORD")
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.authenticatedRequest(context.Background(), http.MethodPatch, "/api/v1/usage/identities/17", []byte(`{"alias":"new name"}`))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != status || patches.Load() != 2 || logins.Load() != 1 {
				t.Fatalf("status=%d patches=%d logins=%d", resp.StatusCode, patches.Load(), logins.Load())
			}
		})
	}
}
