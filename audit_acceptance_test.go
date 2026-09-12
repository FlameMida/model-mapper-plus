package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// The fixture is opt-in and loopback-only. Its exit is not a browser assertion.
func TestAdminAcceptanceServe(t *testing.T) {
	if os.Getenv("MAPPER_ACCEPTANCE_SERVE") != "1" {
		t.Skip("opt-in local browser fixture")
	}
	var mu sync.Mutex
	alias := "验收订阅"
	keeper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/usage/api-keys/settings" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
			return
		}
		if r.Method == http.MethodPatch && r.URL.Path == "/api/v1/usage/identities/17" {
			if r.Header.Get("X-CPA-Usage-Keeper-Request") != "fetch" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			var body struct{ Alias string }
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			alias = body.Alias
		} else if r.Method != http.MethodGet || r.URL.Path != "/api/v1/usage/identities" {
			http.NotFound(w, r)
			return
		}
		display := alias
		if display == "" {
			display = "原名称"
		}
		row := map[string]any{"id": "17", "identity": "idx-a", "auth_type": 1, "type": "codex",
			"provider": "codex", "alias": alias, "displayName": display, "name": "原名称", "is_deleted": false}
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"identities": []any{row}})
		} else {
			_ = json.NewEncoder(w).Encode(row)
		}
	}))
	defer keeper.Close()
	statePath := setupManagementTest(t, Config{Enabled: true, UsageKeeperURL: keeper.URL})
	seed := managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost,
		Body: []byte(`{"key":"fake-client-key","alias":"验收Key","enabled":true,"channel_target":{"enabled":true,"auth_ids":["auth-a"]}}`)})
	if seed.StatusCode != http.StatusOK {
		t.Fatalf("seed status=%d", seed.StatusCode)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/management/api-keys", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"api-keys": []string{"fake-client-key"}})
	})
	mux.HandleFunc("/v0/management/auth-files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"files": []any{map[string]any{
			"id": "auth-a", "auth_index": "idx-a", "name": "auth-a", "provider": "codex",
			"type": "codex", "status": "active", "disabled": false, "label": "原名称"}}})
	})
	for _, name := range []string{"gemini-api-key", "interactions-api-key", "claude-api-key", "codex-api-key", "xai-api-key", "openai-compatibility", "vertex-api-key"} {
		mux.HandleFunc("/v0/management/"+name, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{strings.TrimPrefix(r.URL.Path, "/v0/management/"): []any{}})
		})
	}
	mux.HandleFunc("/", adminAcceptanceHandler)
	listener, err := net.Listen("tcp", "127.0.0.1:31819")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux}
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Logf("UI=http://127.0.0.1:31819/v0/resource/plugins/model-mapper-plus/index.html state=%s keeper=%s", statePath, keeper.URL)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case err := <-done:
		t.Fatalf("server ended: %v", err)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		t.Error(err)
	}
}

func adminAcceptanceHandler(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read failed", http.StatusBadRequest)
		return
	}
	response := dispatchManagement(pluginapi.ManagementRequest{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Headers: r.Header, Body: raw,
	})
	for name, values := range response.Headers {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(response.Body)
}
