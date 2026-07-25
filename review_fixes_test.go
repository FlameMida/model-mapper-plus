package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// H1: host 转发完整 /v0/management/... 路径
func TestDispatchManagementRealHostPath(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true, GlobalRules: "a=>b"})
	resp := dispatchManagement(pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/management/plugins/model-mapper/state",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
}

// H2: 非法 PATCH 不得污染内存态
func TestApplyStateUpdateInvalidPatchDoesNotCorruptMemory(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	ok := managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-a","alias":"A","enabled":true,"rules":{"global":"x=>y"}}`),
	})
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("setup post status=%d body=%s", ok.StatusCode, ok.Body)
	}

	bad := managementPatchKey(pluginapi.ManagementRequest{
		Method: http.MethodPatch,
		Query:  url.Values{"key": {"sk-a"}},
		Body:   []byte(`{"rules":{"global":"a => b"}}`),
	})
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", bad.StatusCode, bad.Body)
	}

	st, _ := loadedStateSnapshot()
	if len(st.KeyBindings) != 1 {
		t.Fatalf("bindings len=%d", len(st.KeyBindings))
	}
	if st.KeyBindings[0].Rules.Global != "x=>y" {
		t.Fatalf("in-memory state corrupted after rejected patch: %+v", st.KeyBindings[0])
	}
}

// H3: loadedRuleSource 返回的切片不得与 holder 共享
func TestLoadedRuleSourceReturnsCopy(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-a","enabled":true,"rules":{"global":"x=>y"}}`),
	})
	src := loadedRuleSource()
	if len(src.KeyBindings) != 1 {
		t.Fatalf("bindings=%d", len(src.KeyBindings))
	}
	src.KeyBindings[0].Rules.Global = "mutated=>z"
	src.KeyBindings[0].Key = "sk-mutated"

	st, _ := loadedStateSnapshot()
	if st.KeyBindings[0].Rules.Global != "x=>y" || st.KeyBindings[0].Key != "sk-a" {
		t.Fatalf("holder mutated via shared slice: %+v", st.KeyBindings[0])
	}
}

// M1: 非法 DSL 的 state_file 不得进入运行态
func TestResolveStateRejectsInvalidDSL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// valid JSON, invalid DSL (whitespace in rule)
	raw := `{"version":1,"rules":{"global":"a => b"},"key_bindings":[]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Enabled: true, GlobalRules: "seed=>ok", StateFile: path}
	holder := resolveState(cfg)
	if holder.persisted {
		t.Fatal("invalid DSL state_file must not be treated as persisted truth")
	}
	if holder.src.Rules.Global != "seed=>ok" {
		t.Fatalf("want YAML seed fallback, got %q", holder.src.Rules.Global)
	}
}

// M2: PATCH 目标不存在不得落盘/创建 state_file
func TestPatchMissingKeyDoesNotCreateStateFile(t *testing.T) {
	statePath := setupManagementTest(t, Config{Enabled: true, GlobalRules: "a=>b"})
	resp := managementPatchKey(pluginapi.ManagementRequest{
		Method: http.MethodPatch,
		Query:  url.Values{"key": {"sk-absent"}},
		Body:   []byte(`{"enabled":false}`),
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want 404", resp.StatusCode)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state file must not be created on missing key patch, err=%v", err)
	}
	st, persisted := loadedStateSnapshot()
	if persisted {
		t.Fatal("runtime must remain unpersisted after missing-key patch")
	}
	if st.Rules.Global != "a=>b" {
		t.Fatalf("seed rules changed: %+v", st.Rules)
	}
}

// M2: DELETE 目标不存在同理
func TestDeleteMissingKeyDoesNotCreateStateFile(t *testing.T) {
	statePath := setupManagementTest(t, Config{Enabled: true, GlobalRules: "a=>b"})
	resp := managementDeleteKey(pluginapi.ManagementRequest{
		Method: http.MethodDelete,
		Query:  url.Values{"key": {"sk-absent"}},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want 404", resp.StatusCode)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state file must not be created on missing key delete, err=%v", err)
	}
}
