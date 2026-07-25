package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

func TestResolveStatePathDefaultAndAbs(t *testing.T) {
	// Force a known self-library path so the default is next to the "plugin".
	pluginDir := t.TempDir()
	selfLibraryPathForTest = filepath.Join(pluginDir, "model-mapper-plus.so")
	t.Cleanup(func() { selfLibraryPathForTest = "" })

	abs, err := ResolveStatePath("")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(abs) {
		t.Fatalf("want absolute path, got %q", abs)
	}
	if filepath.Base(abs) != defaultStateFile {
		t.Fatalf("base = %q, want %q", filepath.Base(abs), defaultStateFile)
	}
	if filepath.Dir(abs) != pluginDir {
		t.Fatalf("dir = %q, want plugin dir %q", filepath.Dir(abs), pluginDir)
	}

	custom := filepath.Join(t.TempDir(), "sub", "custom.json")
	got, err := ResolveStatePath(custom)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) || filepath.Base(got) != "custom.json" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveStatePathSkipsWindowsShadowDir(t *testing.T) {
	shadow := filepath.Join(os.TempDir(), "cliproxy-pluginhost", "pid-12345")
	selfLibraryPathForTest = filepath.Join(shadow, "model-mapper-plus.dll")
	t.Cleanup(func() { selfLibraryPathForTest = "" })

	abs, err := ResolveStatePath("")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(abs) == shadow {
		t.Fatalf("must not place state next to shadow copy: %q", abs)
	}
	if filepath.Base(abs) != defaultStateFile {
		t.Fatalf("base = %q", filepath.Base(abs))
	}
	// Prefer <exe>/plugins/<goos>/<goarch>/ when shadow is skipped.
	if !strings.Contains(abs, filepath.Join("plugins", runtime.GOOS, runtime.GOARCH)) {
		// May fall back to cwd if Executable fails in tests — still must not be shadow.
		t.Logf("resolved (non-shadow) path: %s", abs)
	}
}

func TestIsPluginShadowDir(t *testing.T) {
	marker := filepath.Join(os.TempDir(), "cliproxy-pluginhost")
	if !isPluginShadowDir(marker) {
		t.Fatal("expected marker itself to match")
	}
	if !isPluginShadowDir(filepath.Join(marker, "pid-1")) {
		t.Fatal("expected nested shadow dir to match")
	}
	if isPluginShadowDir(filepath.Join(os.TempDir(), "other")) {
		t.Fatal("unrelated temp subdir must not match")
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
