package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	stateVersion     = 1
	defaultStateFile = "model-mapper-plus-state.json"
)

// ResolveStatePath returns an absolute path for the plugin state file.
//
// Mirrors key-policy's ResolveStatePath: empty input uses defaultStateFile;
// relative paths (including the default) are resolved against the CPA process
// working directory; absolute paths are cleaned as-is. The plugin cannot read
// CPA's plugins.dir, so it does not chase the .so location — set an explicit
// absolute state_file when you need a specific directory.
func ResolveStatePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultStateFile
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// RuleSet mirrors the four top-level rule fields; the same shape is reused
// for per-key bindings (ADR-0003 symmetric segment selection).
type RuleSet struct {
	Global string `json:"global"`
	Claude string `json:"claude"`
	Codex  string `json:"codex"`
	OpenAI string `json:"openai"`
}

type KeyBinding struct {
	Key     string  `json:"key"`
	Alias   string  `json:"alias"`
	Enabled bool    `json:"enabled"`
	Blocked bool    `json:"blocked"`
	Rules   RuleSet `json:"rules"`
}

type State struct {
	Version     int          `json:"version"`
	Rules       RuleSet      `json:"rules"`
	KeyBindings []KeyBinding `json:"key_bindings"`
	UpdatedAt   string       `json:"updated_at,omitempty"`
}

// ruleSource is the runtime view consumed by routeModel/preview.
type ruleSource struct {
	Rules       RuleSet
	KeyBindings []KeyBinding
}

func ruleSetFromConfig(cfg Config) RuleSet {
	return RuleSet{Global: cfg.GlobalRules, Claude: cfg.ClaudeMessagesRules, Codex: cfg.CodexResponsesRules, OpenAI: cfg.OpenAICompletionsRules}
}

func seedStateFromConfig(cfg Config) State {
	return State{Version: stateVersion, Rules: ruleSetFromConfig(cfg)}
}

func ruleSourceFromState(st State) ruleSource {
	return ruleSource{Rules: st.Rules, KeyBindings: st.KeyBindings}
}

func ruleSourceFromConfig(cfg Config) ruleSource {
	return ruleSourceFromState(seedStateFromConfig(cfg))
}

func readStateFile(path string) (State, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, fmt.Errorf("parse state file: %w", err)
	}
	if st.Version != stateVersion {
		return State{}, fmt.Errorf("unsupported state version %d", st.Version)
	}
	return st, nil
}

func validateState(st State) error {
	segments := []struct{ name, rules string }{
		{"rules.global", st.Rules.Global}, {"rules.claude", st.Rules.Claude},
		{"rules.codex", st.Rules.Codex}, {"rules.openai", st.Rules.OpenAI},
	}
	seen := map[string]bool{}
	for i, b := range st.KeyBindings {
		key := strings.TrimSpace(b.Key)
		if key == "" {
			return fmt.Errorf("key_bindings[%d]: key is required", i)
		}
		if seen[key] {
			return fmt.Errorf("key_bindings[%d]: duplicate key", i)
		}
		seen[key] = true
		prefix := fmt.Sprintf("key_bindings[%d].rules", i)
		segments = append(segments,
			struct{ name, rules string }{prefix + ".global", b.Rules.Global},
			struct{ name, rules string }{prefix + ".claude", b.Rules.Claude},
			struct{ name, rules string }{prefix + ".codex", b.Rules.Codex},
			struct{ name, rules string }{prefix + ".openai", b.Rules.OpenAI},
		)
	}
	for _, seg := range segments {
		if seg.rules == "" {
			continue
		}
		if _, err := parseRules(seg.rules); err != nil {
			return fmt.Errorf("%s: %w", seg.name, err)
		}
	}
	return nil
}

func atomicWriteState(path string, st State) error {
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, ".model-mapper-plus-state-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// findKeyBinding returns the enabled binding for apiKey using constant-time
// comparison; empty apiKey never matches.
func findKeyBinding(bindings []KeyBinding, apiKey string) (KeyBinding, bool) {
	if apiKey == "" {
		return KeyBinding{}, false
	}
	for _, b := range bindings {
		if b.Enabled && subtle.ConstantTimeCompare([]byte(b.Key), []byte(apiKey)) == 1 {
			return b, true
		}
	}
	return KeyBinding{}, false
}

// findBlockedKeyBinding returns a blocked binding for apiKey using the same
// constant-time key comparison as findKeyBinding. Enabled is intentionally
// ignored because rule application and access denial are orthogonal.
func findBlockedKeyBinding(bindings []KeyBinding, apiKey string) (KeyBinding, bool) {
	if apiKey == "" {
		return KeyBinding{}, false
	}
	for _, b := range bindings {
		if b.Blocked && subtle.ConstantTimeCompare([]byte(b.Key), []byte(apiKey)) == 1 {
			return b, true
		}
	}
	return KeyBinding{}, false
}

func cloneKeyBindings(in []KeyBinding) []KeyBinding {
	if in == nil {
		return nil
	}
	out := make([]KeyBinding, len(in))
	copy(out, in)
	return out
}

func cloneState(st State) State {
	st.KeyBindings = cloneKeyBindings(st.KeyBindings)
	return st
}

func cloneRuleSource(src ruleSource) ruleSource {
	src.KeyBindings = cloneKeyBindings(src.KeyBindings)
	return src
}

// errKeyBindingNotFound is returned from mutate when the target key disappeared.
var errKeyBindingNotFound = errors.New("key binding not found")

// statePersistError wraps disk/atomic-write failures so management maps them to 500.
type statePersistError struct{ err error }

func (e *statePersistError) Error() string {
	if e == nil || e.err == nil {
		return "state persist failed"
	}
	return e.err.Error()
}

func (e *statePersistError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}
