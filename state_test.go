package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedStateFromConfig(t *testing.T) {
	cfg := Config{GlobalRules: "g1", ClaudeMessagesRules: "c1"}
	st := seedStateFromConfig(cfg)
	if st.Version != stateVersion {
		t.Fatalf("version = %d, want %d", st.Version, stateVersion)
	}
	if st.Rules.Global != "g1" || st.Rules.Claude != "c1" || st.Rules.Codex != "" || st.Rules.OpenAI != "" {
		t.Fatalf("unexpected seed rules: %+v", st.Rules)
	}
	if len(st.KeyBindings) != 0 {
		t.Fatalf("seed key bindings = %v, want empty", st.KeyBindings)
	}
}

// Scenario: seed 仅一次（读取侧：state_file 存在时 YAML 不再生效）
func TestReadStateFileTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st := State{Version: stateVersion, Rules: RuleSet{Global: "from-state"}, KeyBindings: []KeyBinding{{Key: "sk-a", Enabled: true}}}
	if err := atomicWriteState(path, st); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readStateFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Rules.Global != "from-state" || len(got.KeyBindings) != 1 {
		t.Fatalf("unexpected state: %+v", got)
	}
}

// Scenario: 非法 JSON 回退（readStateFile 报错，调用方回退 seed）
func TestReadStateFileCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readStateFile(path); err == nil {
		t.Fatal("want error for corrupt state file")
	}
}

func TestReadStateFileUnknownVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"rules":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readStateFile(path); err == nil {
		t.Fatal("want error for unknown version")
	}
}

// Scenario: 任意时刻文件完整（原子写产物合法且权限 0600）
func TestAtomicWriteStatePermAndValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st := State{Version: stateVersion, Rules: RuleSet{Global: "g"}}
	if err := atomicWriteState(path, st); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o, want 600", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	var decoded State
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("written file not valid json: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp files left behind: %d entries", len(entries))
	}
}

func TestValidateState(t *testing.T) {
	cases := []struct {
		name    string
		st      State
		wantErr string
	}{
		{"ok", State{Version: stateVersion, Rules: RuleSet{Global: "a=>b"}}, ""},
		{"bad top-level rules", State{Version: stateVersion, Rules: RuleSet{Global: "a => b"}}, "rules.global"},
		{"bad binding rules", State{Version: stateVersion, KeyBindings: []KeyBinding{{Key: "sk-a", Rules: RuleSet{Claude: "x => y"}}}}, "claude"},
		{"empty binding key", State{Version: stateVersion, KeyBindings: []KeyBinding{{Key: "  "}}}, "key"},
		{"duplicate binding key", State{Version: stateVersion, KeyBindings: []KeyBinding{{Key: "sk-a"}, {Key: "sk-a"}}}, "duplicate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateState(tc.st)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// Scenario: 默认路径基于进程 cwd（与 key-policy ResolveStatePath 对齐）
func TestResolveStatePathDefaultAndAbs(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	abs, err := ResolveStatePath("")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wd, defaultStateFile); abs != want {
		t.Fatalf("ResolveStatePath(\"\") = %q, want %q (cwd-based)", abs, want)
	}
	// explicit relative name resolves against cwd too
	got, err := ResolveStatePath("sub/state.json")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wd, "sub", "state.json"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// absolute passthrough (cleaned)
	absIn := filepath.Join(wd, "abs.json")
	got, err = ResolveStatePath(absIn)
	if err != nil {
		t.Fatal(err)
	}
	if got != absIn {
		t.Fatalf("absolute not preserved: got %q", got)
	}
}

func TestAtomicWriteStateCreatesParentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	path := filepath.Join(dir, "state.json")
	st := State{Version: stateVersion, Rules: RuleSet{Global: "a=>b"}}
	if err := atomicWriteState(path, st); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readStateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rules.Global != "a=>b" {
		t.Fatalf("got %+v", got)
	}
}

// Scenario: 旧 state 加载后 blocked 为 false。
func TestOldStateWithoutBlockedDefaultsFalseAndStillRoutesEnabledBinding(t *testing.T) {
	raw := []byte(`{"version":1,"rules":{},"key_bindings":[{"key":"sk-a","enabled":true,"rules":{"global":"a=>b"}}]}`)
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("unmarshal old state: %v", err)
	}
	if len(st.KeyBindings) != 1 {
		t.Fatalf("bindings = %d, want 1", len(st.KeyBindings))
	}
	if st.KeyBindings[0].Blocked {
		t.Fatal("missing blocked field must decode as false")
	}
	decision, err := routeModel(testConfig(), ruleSourceFromState(st), "openai", "a", "sk-a")
	if err != nil {
		t.Fatalf("routeModel: %v", err)
	}
	if !decision.Handled || decision.UpstreamModel != "b" {
		t.Fatalf("old enabled binding no longer routes: %+v", decision)
	}
}

func TestFindBlockedKeyBindingIgnoresEnabled(t *testing.T) {
	bindings := []KeyBinding{
		{Key: "sk-blocked-rules-off", Enabled: false, Blocked: true},
		{Key: "sk-allowed", Enabled: true, Blocked: false},
	}
	got, ok := findBlockedKeyBinding(bindings, "sk-blocked-rules-off")
	if !ok || got.Key != "sk-blocked-rules-off" {
		t.Fatalf("blocked binding must match with enabled=false: got=%+v ok=%v", got, ok)
	}
	if _, ok := findBlockedKeyBinding(bindings, "sk-allowed"); ok {
		t.Fatal("blocked=false must not match")
	}
	if _, ok := findBlockedKeyBinding(bindings, ""); ok {
		t.Fatal("empty api key must not match")
	}
	if _, ok := findBlockedKeyBinding(bindings, "sk-missing"); ok {
		t.Fatal("unknown api key must not match")
	}
}
