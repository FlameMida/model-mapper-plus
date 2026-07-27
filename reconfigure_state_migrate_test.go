package main

import (
	"os"
	"path/filepath"
	"testing"
)

// reconfigure 把 state_file 换到一个不存在的新路径时，若当前已有持久化数据，
// SHALL 把当前数据迁移到新路径（而非回退空 seed）—— 否则用户换路径会"丢"数据。
// 首次运行（无持久化数据）换到不存在的新路径仍回退 seed，不创建文件（ADR-0001 不变）。
func TestReconfigureMigratesPersistedStateToNewNonexistentPath(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.json")
	pathB := filepath.Join(dir, "b.json") // 不存在

	stateA := State{
		Version:     stateVersion,
		Rules:       RuleSet{Global: "a=>a-dst"},
		KeyBindings: []KeyBinding{{Key: "sk-a", Enabled: true, Rules: RuleSet{Global: "k=>k-dst"}}},
	}
	if err := atomicWriteState(pathA, stateA); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })

	// 先 reconfigure 到 A，建立持久化状态。
	if _, err := handlePluginReconfigure(lifecycleRaw(t, "enabled: true\nstate_file: "+pathA+"\n")); err != nil {
		t.Fatalf("reconfigure to A: %v", err)
	}
	if _, persisted := loadedStateSnapshot(); !persisted {
		t.Fatal("A should be persisted")
	}
	if _, err := os.Stat(pathB); !os.IsNotExist(err) {
		t.Fatal("B should not exist yet")
	}

	// reconfigure 到不存在的新路径 B —— 应把 A 的数据迁移过去。
	if _, err := handlePluginReconfigure(lifecycleRaw(t, "enabled: true\nstate_file: "+pathB+"\n")); err != nil {
		t.Fatalf("reconfigure to B: %v", err)
	}

	src := loadedRuleSource()
	if src.Rules.Global != "a=>a-dst" {
		t.Fatalf("rules not migrated, got %#v", src.Rules)
	}
	if len(src.KeyBindings) != 1 || src.KeyBindings[0].Key != "sk-a" || src.KeyBindings[0].Rules.Global != "k=>k-dst" {
		t.Fatalf("key binding not migrated, got %#v", src.KeyBindings)
	}
	if _, err := os.Stat(pathB); err != nil {
		t.Fatalf("B should be created by migration: %v", err)
	}
	if _, persisted := loadedStateSnapshot(); !persisted {
		t.Fatal("should be persisted after migration")
	}
	if got := stateFilePath(); got != pathB {
		t.Fatalf("stateFilePath = %q, want %q", got, pathB)
	}
}

// 首次运行（无持久化数据）换到不存在的新路径，SHALL 回退 seed 且不创建文件（ADR-0001 不变）。
func TestReconfigureDoesNotMigrateOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	pathB := filepath.Join(dir, "first-run.json") // 不存在

	t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })
	setLoadedConfigForTest(defaultConfig()) // 确保起始为非持久化的 default seed
	if _, persisted := loadedStateSnapshot(); persisted {
		t.Fatal("precondition: should start non-persisted")
	}

	if _, err := handlePluginReconfigure(lifecycleRaw(t, "enabled: true\nstate_file: "+pathB+"\n")); err != nil {
		t.Fatalf("reconfigure: %v", err)
	}

	if _, err := os.Stat(pathB); !os.IsNotExist(err) {
		t.Fatalf("first run must not create state file, got err=%v", err)
	}
	if _, persisted := loadedStateSnapshot(); persisted {
		t.Fatal("first run must stay non-persisted")
	}
}

// 换到一个已存在且有数据的路径，SHALL 直接读它（不触发迁移）。
func TestReconfigureSwitchesToExistingPathReadsIt(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.json")
	pathB := filepath.Join(dir, "b.json")

	if err := atomicWriteState(pathA, State{Version: stateVersion, Rules: RuleSet{Global: "a=>a"}, KeyBindings: []KeyBinding{{Key: "sk-a", Enabled: true}}}); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	if err := atomicWriteState(pathB, State{Version: stateVersion, Rules: RuleSet{Global: "b=>b"}, KeyBindings: []KeyBinding{{Key: "sk-b", Enabled: true}}}); err != nil {
		t.Fatalf("seed B: %v", err)
	}
	t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })

	if _, err := handlePluginReconfigure(lifecycleRaw(t, "enabled: true\nstate_file: "+pathA+"\n")); err != nil {
		t.Fatalf("reconfigure to A: %v", err)
	}
	if _, err := handlePluginReconfigure(lifecycleRaw(t, "enabled: true\nstate_file: "+pathB+"\n")); err != nil {
		t.Fatalf("reconfigure to B: %v", err)
	}

	src := loadedRuleSource()
	if src.Rules.Global != "b=>b" || len(src.KeyBindings) != 1 || src.KeyBindings[0].Key != "sk-b" {
		t.Fatalf("did not read existing B: %#v", src)
	}
}
