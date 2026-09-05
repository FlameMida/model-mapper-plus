package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestManagementKeeperConfigurationAndRegistration(t *testing.T) {
	cfg, err := decodeConfig([]byte(`{"usage_keeper_url":"http://keeper:8080/base","usage_keeper_password_env":"KEEPER_PASS"}`))
	if err != nil || cfg.UsageKeeperURL != "http://keeper:8080/base" || cfg.UsageKeeperPasswordEnv != "KEEPER_PASS" {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
	if defaultConfig().UsageKeeperPasswordEnv != "CPA_KEEPER_LOGIN_PASSWORD" {
		t.Fatal("missing default env")
	}
	raw, err := handleManagementRegister()
	if err != nil {
		t.Fatal(err)
	}
	var reg pluginapi.ManagementRegistrationResponse
	_ = json.Unmarshal(raw, &reg)
	found := map[string]bool{}
	for _, route := range reg.Routes {
		found[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{"GET /plugins/model-mapper-plus/keeper/key-aliases", "POST /plugins/model-mapper-plus/keeper/key-aliases/refresh"} {
		if !found[route] {
			t.Fatalf("missing %s", route)
		}
	}
}

func TestManagementKeeperFailureDoesNotChangeBindingState(t *testing.T) {
	resetKeeperAliases()
	defer resetKeeperAliases()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer server.Close()
	path := setupManagementTest(t, Config{Enabled: true, UsageKeeperURL: server.URL})
	managementPostKey(pluginapi.ManagementRequest{Method: "POST", Body: []byte(`{"key":"sk-a","alias":"Local","enabled":true,"rules":{"global":"a=>b"}}`)})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	resp := dispatchManagement(pluginapi.ManagementRequest{Method: "GET", Path: managementHandleBase + "/keeper/key-aliases"})
	if resp.StatusCode != 200 || resp.Headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("response=%+v", resp)
	}
	var body keeperAliasesResponse
	decodeBody(t, resp, &body)
	if body.Status != "unavailable" || body.ErrorCode != "authentication_failed" {
		t.Fatalf("body=%+v", body)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("keeper request wrote state")
	}
	if got := managementGetState(); got.StatusCode != 200 {
		t.Fatal("state unavailable")
	}
}

func TestManagementKeeperDisabledAndInvalidConfiguration(t *testing.T) {
	resetKeeperAliases()
	defer resetKeeperAliases()
	for _, tc := range []struct{ url, status, code string }{{"", "disabled", ""}, {"file:///tmp/private", "unavailable", "configuration_error"}} {
		setupManagementTest(t, Config{Enabled: true, UsageKeeperURL: tc.url})
		resp := dispatchManagement(pluginapi.ManagementRequest{Method: "POST", Path: managementHandleBase + "/keeper/key-aliases/refresh"})
		var body keeperAliasesResponse
		decodeBody(t, resp, &body)
		if resp.StatusCode != 200 || body.Status != tc.status || body.ErrorCode != tc.code || body.Items == nil {
			t.Fatalf("response=%+v body=%+v", resp, body)
		}
	}
}

func TestKeeperLifecycleYAMLFields(t *testing.T) {
	path := setupManagementTest(t, defaultConfig())
	for _, handler := range []func([]byte) ([]byte, error){handlePluginRegister, handlePluginReconfigure} {
		_, err := handler(lifecycleRaw(t, "enabled: true\nstate_file: "+path+"\nusage_keeper_url: http://keeper:8080/base\nusage_keeper_password_env: KEEPER_TEST_PASSWORD\n"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := loadedConfig()
		if cfg.UsageKeeperURL != "http://keeper:8080/base" || cfg.UsageKeeperPasswordEnv != "KEEPER_TEST_PASSWORD" {
			t.Fatalf("YAML fields lost: %+v", cfg)
		}
	}
}

func TestKeeperPasswordEnvBlankUsesDefault(t *testing.T) {
	cfg, err := decodeConfig([]byte(`{"usage_keeper_password_env":"  "}`))
	if err != nil || cfg.UsageKeeperPasswordEnv != "CPA_KEEPER_LOGIN_PASSWORD" {
		t.Fatalf("blank env=%q err=%v", cfg.UsageKeeperPasswordEnv, err)
	}
}

func TestKeeperAliasesRejectsSnapshotTakenBeforeReconfiguration(t *testing.T) {
	var calls int
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(`{"items":[]}`)) }))
	defer old.Close()
	oldConfig := Config{Enabled: true, UsageKeeperURL: old.URL}
	setupManagementTest(t, oldConfig)
	snapshot := loadedConfig()
	setLoadedConfigForTest(defaultConfig())
	got := keeperAliasesForConfig(snapshot, true)
	if got.Status == "ready" || calls != 0 {
		t.Fatalf("stale snapshot restored old service: %+v calls=%d", got, calls)
	}
}

func TestKeeperInFlightDoesNotBlockStateReadOrSave(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()
	defer close(release)
	setupManagementTest(t, Config{Enabled: true, UsageKeeperURL: server.URL})
	go managementKeeperAliases(true)
	<-entered
	finished := make(chan bool, 1)
	go func() {
		read := managementGetState()
		save := managementPostKey(pluginapi.ManagementRequest{Body: []byte(`{"key":"sk-independent","alias":"Local","enabled":true}`)})
		finished <- read.StatusCode == 200 && save.StatusCode < 300
	}()
	select {
	case ok := <-finished:
		if !ok {
			t.Fatal("state operation failed")
		}
	case <-time.After(time.Second):
		t.Fatal("Keeper IO blocked state operations")
	}
}
