package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func setupManagementTest(t *testing.T, cfg Config) string {
	t.Helper()
	statePath := t.TempDir() + "/state.json"
	cfg.StateFile = statePath
	setLoadedConfigForTest(cfg)
	loadedStateMu.Lock()
	loadedHolder = resolveState(cfg)
	loadedStateMu.Unlock()
	return statePath
}

func decodeBody(t *testing.T, resp pluginapi.ManagementResponse, v any) {
	t.Helper()
	if err := json.Unmarshal(resp.Body, v); err != nil {
		t.Fatalf("decode body: %v (%s)", err, resp.Body)
	}
}

func TestManagementGetStateSeedsFromYAML(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true, GlobalRules: "a=>b", StateFile: ""})
	resp := managementGetState()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Rules     RuleSet `json:"rules"`
		Persisted bool    `json:"persisted"`
	}
	decodeBody(t, resp, &body)
	if body.Persisted {
		t.Fatal("want persisted=false before first save")
	}
	if body.Rules.Global != "a=>b" {
		t.Fatalf("rules = %+v", body.Rules)
	}
}

// Scenario: seed 仅一次
func TestManagementFirstSaveCreatesStateFile(t *testing.T) {
	statePath := setupManagementTest(t, Config{Enabled: true, GlobalRules: "a=>b"})
	resp := managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-a","alias":"A","enabled":true,"rules":{"global":"x=>y"}}`),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, resp.Body)
	}
	st, err := readStateFile(statePath)
	if err != nil {
		t.Fatalf("state file not created: %v", err)
	}
	if st.Rules.Global != "a=>b" {
		t.Fatalf("seed rules lost: %+v", st.Rules)
	}
	if len(st.KeyBindings) != 1 || st.KeyBindings[0].Key != "sk-a" {
		t.Fatalf("bindings = %+v", st.KeyBindings)
	}
	// 此后改 YAML 不再生效
	setLoadedConfigForTest(Config{Enabled: true, GlobalRules: "g2", StateFile: statePath})
	loadedStateMu.Lock()
	loadedHolder = resolveState(loadedConfig())
	loadedStateMu.Unlock()
	if got := loadedRuleSource().Rules.Global; got != "a=>b" {
		t.Fatalf("yaml override leaked: %q", got)
	}
}

// Scenario: 非法规则拒绝且状态不变
func TestManagementPutRulesRejectsInvalid(t *testing.T) {
	statePath := setupManagementTest(t, Config{Enabled: true, GlobalRules: "a=>b"})
	resp := managementPutRules(pluginapi.ManagementRequest{
		Method: http.MethodPut,
		Body:   []byte(`{"global":"gpt-* => deepseek"}`),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if _, err := readStateFile(statePath); err == nil {
		t.Fatal("state file must not be created on rejected save")
	}
	var body map[string]string
	decodeBody(t, resp, &body)
	if body["error"] == "" {
		t.Fatal("missing error description")
	}
}

func TestManagementPostKeyUpsert(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(`{"key":"sk-a","enabled":true,"rules":{"global":"x=>y"}}`)})
	resp := managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(`{"key":"sk-a","alias":"B","enabled":true,"rules":{"global":"p=>q"}}`)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	st, _ := loadedStateSnapshot()
	if len(st.KeyBindings) != 1 || st.KeyBindings[0].Alias != "B" || st.KeyBindings[0].Rules.Global != "p=>q" {
		t.Fatalf("upsert failed: %+v", st.KeyBindings)
	}
}

func TestManagementPatchKey(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(`{"key":"sk-a","alias":"A","enabled":true,"rules":{"global":"x=>y"}}`)})
	resp := managementPatchKey(pluginapi.ManagementRequest{
		Method: http.MethodPatch,
		Query:  url.Values{"key": {"sk-a"}},
		Body:   []byte(`{"enabled":false}`),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, resp.Body)
	}
	st, _ := loadedStateSnapshot()
	b := st.KeyBindings[0]
	if b.Enabled || b.Alias != "A" || b.Rules.Global != "x=>y" {
		t.Fatalf("patch clobbered fields: %+v", b)
	}
}

func TestManagementDeleteKey(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(`{"key":"sk-a","enabled":true}`)})
	resp := managementDeleteKey(pluginapi.ManagementRequest{
		Method: http.MethodDelete,
		Query:  url.Values{"key": {"sk-a"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	st, _ := loadedStateSnapshot()
	if len(st.KeyBindings) != 0 {
		t.Fatalf("delete failed: %+v", st.KeyBindings)
	}
}

func TestManagementPatchKeyMissing(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	resp := managementPatchKey(pluginapi.ManagementRequest{
		Method: http.MethodPatch,
		Query:  url.Values{"key": {"sk-absent"}},
		Body:   []byte(`{"enabled":false}`),
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// Scenario: 非法 JSON 回退（resolveState 损坏时以 seed 运行，保存可修复）
func TestManagementRecoversFromCorruptState(t *testing.T) {
	cfg := Config{Enabled: true, GlobalRules: "a=>b"}
	statePath := t.TempDir() + "/state.json"
	cfg.StateFile = statePath
	setLoadedConfigForTest(cfg)
	if err := os.WriteFile(statePath, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	loadedStateMu.Lock()
	loadedHolder = resolveState(cfg)
	loadedStateMu.Unlock()
	if got := loadedRuleSource().Rules.Global; got != "a=>b" {
		t.Fatalf("seed fallback failed: %q", got)
	}
	resp := managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(`{"key":"sk-a","enabled":true}`)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("repair save failed: %d", resp.StatusCode)
	}
	if _, err := readStateFile(statePath); err != nil {
		t.Fatalf("state not repaired: %v", err)
	}
}

// Scenario: /state 返回插件版本且不持久化
func TestManagementGetStateIncludesPluginVersion(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	resp := managementGetState()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		PluginVersion string `json:"plugin_version"`
	}
	decodeBody(t, resp, &body)
	if body.PluginVersion != pluginVersion {
		t.Fatalf("plugin_version = %q, want %q", body.PluginVersion, pluginVersion)
	}
	if body.PluginVersion == "" {
		t.Fatal("plugin_version must be non-empty (CPA rejects empty Version)")
	}
}

// Scenario: plugin_version 不写 state_file（仅响应层）
func TestStateFileExcludesPluginVersion(t *testing.T) {
	statePath := setupManagementTest(t, Config{Enabled: true, GlobalRules: "a=>b"})
	managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-a","enabled":true,"rules":{"global":"x=>y"}}`),
	})
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("state file not created: %v", err)
	}
	if strings.Contains(string(raw), "plugin_version") {
		t.Fatalf("state_file must not contain plugin_version: %s", raw)
	}
}

// Scenario: blocked 经管理 API 写入后可再读出。
func TestManagementPostKeyPersistsBlockedAndGetStateReadsIt(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	post := managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-a","alias":"A","enabled":true,"blocked":true,"rules":{"global":"x=>y"}}`),
	})
	if post.StatusCode != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", post.StatusCode, post.Body)
	}
	get := managementGetState()
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", get.StatusCode, get.Body)
	}
	var body stateResponse
	decodeBody(t, get, &body)
	if len(body.KeyBindings) != 1 || body.KeyBindings[0].Key != "sk-a" {
		t.Fatalf("GET key_bindings=%+v", body.KeyBindings)
	}
	if !body.KeyBindings[0].Blocked {
		t.Fatalf("GET did not read back blocked=true: %+v", body.KeyBindings[0])
	}
}

func TestManagementPatchKeyUnblocksWithoutClobberingEnabled(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	seed := managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-a","alias":"A","enabled":true,"blocked":true,"rules":{"global":"x=>y"}}`),
	})
	if seed.StatusCode != http.StatusOK {
		t.Fatalf("seed status=%d body=%s", seed.StatusCode, seed.Body)
	}
	patch := managementPatchKey(pluginapi.ManagementRequest{
		Method: http.MethodPatch,
		Query:  url.Values{"key": {"sk-a"}},
		Body:   []byte(`{"blocked":false}`),
	})
	if patch.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status=%d body=%s", patch.StatusCode, patch.Body)
	}
	get := managementGetState()
	var body stateResponse
	decodeBody(t, get, &body)
	got := body.KeyBindings[0]
	if got.Blocked {
		t.Fatalf("blocked=%v, want false after PATCH", got.Blocked)
	}
	if !got.Enabled || got.Alias != "A" || got.Rules.Global != "x=>y" {
		t.Fatalf("PATCH blocked clobbered another field: %+v", got)
	}
}
