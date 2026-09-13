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

type ChannelTarget struct {
	Enabled   bool     `json:"enabled"`
	Suppliers []string `json:"suppliers,omitempty"`
	AuthIDs   []string `json:"auth_ids,omitempty"`
}

type KeyBinding struct {
	Key           string         `json:"key"`
	Alias         string         `json:"alias"`
	Enabled       bool           `json:"enabled"`
	Blocked       bool           `json:"blocked"`
	Rules         RuleSet        `json:"rules"`
	ChannelTarget *ChannelTarget `json:"channel_target,omitempty"`
	FastAllowed   *bool          `json:"fast_allowed,omitempty"`
	Notifications []Notification `json:"notifications,omitempty"`
}

type State struct {
	Version       int                   `json:"version"`
	Rules         RuleSet               `json:"rules"`
	KeyBindings   []KeyBinding          `json:"key_bindings"`
	Notifications *NotificationSettings `json:"notifications,omitempty"`
	UpdatedAt     string                `json:"updated_at,omitempty"`
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

func validateUniqueNonEmptyStrings(path string, values []string, foldCase bool) error {
	seen := make(map[string]struct{}, len(values))
	for i, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return fmt.Errorf("%s[%d]: value is required", path, i)
		}
		key := value
		if foldCase {
			key = strings.ToLower(value)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%s[%d]: duplicate value %q", path, i, value)
		}
		seen[key] = struct{}{}
	}
	return nil
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
		if b.ChannelTarget != nil {
			if err := validateUniqueNonEmptyStrings(fmt.Sprintf("key_bindings[%d].channel_target.suppliers", i), b.ChannelTarget.Suppliers, true); err != nil {
				return err
			}
			if err := validateUniqueNonEmptyStrings(fmt.Sprintf("key_bindings[%d].channel_target.auth_ids", i), b.ChannelTarget.AuthIDs, false); err != nil {
				return err
			}
		}
		if err := validateKeyNotificationsEntity(i, &b); err != nil {
			return err
		}
		prefix := fmt.Sprintf("key_bindings[%d].rules", i)
		segments = append(segments,
			struct{ name, rules string }{prefix + ".global", b.Rules.Global},
			struct{ name, rules string }{prefix + ".claude", b.Rules.Claude},
			struct{ name, rules string }{prefix + ".codex", b.Rules.Codex},
			struct{ name, rules string }{prefix + ".openai", b.Rules.OpenAI},
		)
	}
	if st.Notifications != nil {
		if err := validateNotificationSettings(st.Notifications); err != nil {
			return err
		}
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

// findKeyBindingByKey returns the binding for apiKey using constant-time
// comparison; feature-specific enable flags are intentionally ignored.
func findKeyBindingByKey(bindings []KeyBinding, apiKey string) (KeyBinding, bool) {
	if apiKey == "" {
		return KeyBinding{}, false
	}
	for _, b := range bindings {
		if subtle.ConstantTimeCompare([]byte(b.Key), []byte(apiKey)) == 1 {
			return b, true
		}
	}
	return KeyBinding{}, false
}

// findKeyBinding returns the enabled binding used by rule mapping.
func findKeyBinding(bindings []KeyBinding, apiKey string) (KeyBinding, bool) {
	b, ok := findKeyBindingByKey(bindings, apiKey)
	return b, ok && b.Enabled
}

func findActiveChannelTarget(bindings []KeyBinding, apiKey string) (KeyBinding, bool) {
	b, ok := findKeyBindingByKey(bindings, apiKey)
	if !ok || b.ChannelTarget == nil || !b.ChannelTarget.Enabled {
		return KeyBinding{}, false
	}
	return b, true
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
	for i := range in {
		out[i] = in[i]
		out[i].ChannelTarget = cloneChannelTarget(in[i].ChannelTarget)
		out[i].Notifications = cloneNotifications(in[i].Notifications)
		if in[i].FastAllowed != nil {
			value := *in[i].FastAllowed
			out[i].FastAllowed = &value
		}
	}
	return out
}

func cloneChannelTarget(in *ChannelTarget) *ChannelTarget {
	if in == nil {
		return nil
	}
	out := *in
	out.Suppliers = append([]string(nil), in.Suppliers...)
	out.AuthIDs = append([]string(nil), in.AuthIDs...)
	return &out
}

func cloneState(st State) State {
	st.KeyBindings = cloneKeyBindings(st.KeyBindings)
	return st
}

func cloneRuleSource(src ruleSource) ruleSource {
	src.KeyBindings = cloneKeyBindings(src.KeyBindings)
	return src
}

// cloneNotifications deep-copies a notification list including modules,
// platform identities and the schedule pointer.
func cloneNotifications(in []Notification) []Notification {
	if in == nil {
		return nil
	}
	out := make([]Notification, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Modules = append([]ModuleConfig(nil), in[i].Modules...)
		out[i].Platforms = clonePlatformIdentities(in[i].Platforms)
		if in[i].Schedule != nil {
			s := *in[i].Schedule
			out[i].Schedule = &s
		}
	}
	return out
}

// clonePlatformIdentities deep-copies platform identities including user IDs.
func clonePlatformIdentities(in []PlatformIdentity) []PlatformIdentity {
	if in == nil {
		return nil
	}
	out := make([]PlatformIdentity, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].UserIDs = append([]string(nil), in[i].UserIDs...)
	}
	return out
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
