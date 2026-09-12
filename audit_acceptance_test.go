package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestAdminAcceptanceAuditProcessRestart(t *testing.T) {
	statePath := setupManagementTest(t, Config{Enabled: true})
	response := dispatchManagement(pluginapi.ManagementRequest{
		Method: http.MethodPost, Path: managementHandleBase + "/keys",
		Body: []byte(`{"key":"fake-restart-key","alias":"restart proof","enabled":true}`),
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("create status=%d", response.StatusCode)
	}
	var result struct {
		Audit struct {
			OperationID string `json:"operation_id"`
			Recorded    bool   `json:"recorded"`
		} `json:"audit"`
	}
	decodeBody(t, response, &result)
	if !result.Audit.Recorded || result.Audit.OperationID == "" {
		t.Fatal("operation was not recorded")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(binary, "-test.run=^TestAdminAcceptanceRestartReader$", "-test.v=true")
	child.Env = append(os.Environ(), "MAPPER_AUDIT_RESTART_STATE="+statePath,
		"MAPPER_AUDIT_RESTART_ID="+result.Audit.OperationID)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("fresh process read failed: %v\n%s", err, output)
	}
	t.Logf("Fresh process output:\n%s", output)
}

func TestAdminAcceptanceRestartReader(t *testing.T) {
	path := os.Getenv("MAPPER_AUDIT_RESTART_STATE")
	if path == "" {
		t.Skip("child-process audit reader")
	}
	cfg := Config{Enabled: true, StateFile: path}
	setLoadedConfigForTest(cfg)
	loadedStateMu.Lock()
	loadedHolder = resolveState(cfg)
	loadedStateMu.Unlock()
	response := dispatchManagement(pluginapi.ManagementRequest{Method: http.MethodGet,
		Path: managementHandleBase + "/audit"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("query status=%d", response.StatusCode)
	}
	var history auditPage
	decodeBody(t, response, &history)
	if history.Total != 1 || len(history.Items) != 1 || len(history.Warnings) != 0 {
		t.Fatalf("unexpected persisted history: %+v", history)
	}
	item := history.Items[0]
	if item.OperationID != os.Getenv("MAPPER_AUDIT_RESTART_ID") || item.Outcome != "succeeded" || item.Action != "create" {
		t.Fatalf("persisted event differs: %+v", item)
	}
	var state stateResponse
	decodeBody(t, managementGetState(), &state)
	if len(state.KeyBindings) != 1 || state.KeyBindings[0].Alias != "restart proof" {
		t.Fatal("persisted binding missing from fresh process")
	}
}

// The fixture is opt-in and loopback-only. Its exit is not a browser assertion.
func TestAdminAcceptanceServe(t *testing.T) {
	if os.Getenv("MAPPER_ACCEPTANCE_SERVE") != "1" {
		t.Skip("opt-in local browser fixture")
	}
	var mu sync.Mutex
	alias := "验收订阅"
	fault := ""
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
			mode := fault
			fault = ""
			if mode == "timeout" {
				_, _ = io.Copy(io.Discard, r.Body)
				<-r.Context().Done()
				return
			}
			if mode == "finish_sync" {
				auditIOMu.Lock()
				original := auditOpenFile
				auditOpenFile = func(path string, flag int, perm os.FileMode) (auditFile, error) {
					auditOpenFile = original
					file, err := original(path, flag, perm)
					if err != nil {
						return nil, err
					}
					return acceptanceSyncFailure{auditFile: file}, nil
				}
				auditIOMu.Unlock()
			}
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
	mux.HandleFunc("/fixture-control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct{ Mode string }
		if json.NewDecoder(r.Body).Decode(&body) != nil || (body.Mode != "" && body.Mode != "timeout" && body.Mode != "finish_sync") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		fault = body.Mode
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/fixture", func(w http.ResponseWriter, r *http.Request) {
		theme := "white"
		if r.URL.Query().Get("theme") == "dark" {
			theme = "dark"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html data-theme="`+theme+`"><head><title>Mapper local acceptance</title><style>html,body{margin:0;width:100%;height:100%;overflow:hidden}iframe{border:0;width:100%;height:100vh;display:block}</style></head><body><iframe id="mapper" src="/v0/resource/plugins/model-mapper-plus/index.html"></iframe></body></html>`)
	})
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

type acceptanceSyncFailure struct{ auditFile }

func (acceptanceSyncFailure) Sync() error { return errors.New("acceptance_finish_sync") }

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
