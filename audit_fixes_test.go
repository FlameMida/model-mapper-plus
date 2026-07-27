package main

// Regression tests for the full-project audit. Each test pins the fixed
// behaviour of one finding; the identifiers match the audit report.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func lifecycleRaw(t *testing.T, yaml string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"config_yaml": base64.StdEncoding.EncodeToString([]byte(yaml)),
	})
	if err != nil {
		t.Fatalf("marshal lifecycle: %v", err)
	}
	return raw
}

func decodeLifecycle(t *testing.T, yaml string) Config {
	t.Helper()
	cfgRaw, _, err := decodeLifecycleConfig(lifecycleRaw(t, yaml))
	if err != nil {
		t.Fatalf("decodeLifecycleConfig(%q) error = %v", yaml, err)
	}
	cfg, err := decodeConfig(cfgRaw)
	if err != nil {
		t.Fatalf("decodeConfig(%q) error = %v", yaml, err)
	}
	return cfg
}

// Audit #1: a complete SSE event split mid-line must not bypass the SSE buffer.
// Comparison is semantic: rewriting round-trips the payload through a map, so
// JSON object keys come back in sorted order.
func TestStreamRewriterKeepsOrderWhenEventSplitMidLine(t *testing.T) {
	const original = "orig-model"
	full := "data: {\"model\":\"up\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"
	for _, cut := range []int{5, 12, 26, 40, len(full) - 3} {
		t.Run(fmt.Sprintf("split-at-%d", cut), func(t *testing.T) {
			r := newStreamChunkRewriter(original)
			var got strings.Builder
			for _, part := range []string{full[:cut], full[cut:]} {
				chunks, err := r.Write([]byte(part))
				if err != nil {
					t.Fatalf("Write(%q) error = %v", part, err)
				}
				for _, c := range chunks {
					got.Write(c)
				}
			}
			flushed, err := r.Flush()
			if err != nil {
				t.Fatalf("Flush error = %v", err)
			}
			for _, c := range flushed {
				got.Write(c)
			}
			out := got.String()
			payload, ok := strings.CutPrefix(out, "data: ")
			if !ok {
				t.Fatalf("output does not start with the SSE data field (fragment emitted early?): %q", out)
			}
			payload, ok = strings.CutSuffix(payload, "\n\n")
			if !ok {
				t.Fatalf("output is not one terminated SSE event: %q", out)
			}
			var doc map[string]any
			if err := json.Unmarshal([]byte(payload), &doc); err != nil {
				t.Fatalf("payload is not valid JSON (event corrupted): %q", out)
			}
			if doc["model"] != original {
				t.Fatalf("model=%v, want %q (restoration skipped): %q", doc["model"], original, out)
			}
			choices, _ := json.Marshal(doc["choices"])
			if want := `[{"delta":{"content":"hi"}}]`; string(choices) != want {
				t.Fatalf("choices=%s, want %s", choices, want)
			}
		})
	}
}

// Audit #18: a rewritten event is emitted as one chunk, not one per line.
func TestSSERewriterEmitsOneChunkPerEvent(t *testing.T) {
	r := newSSERewriter("A")
	out, err := r.Write([]byte("event: message\ndata: {\"model\":\"B\"}\nid: 1\n\n"))
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("emitted %d chunks, want 1 per event: %q", len(out), out)
	}
}

// Audit #19: an upstream that never terminates an event cannot grow the buffer
// without bound; past the cap the bytes are released unrewritten.
func TestSSERewriterCapsUnterminatedBuffer(t *testing.T) {
	r := newSSERewriter("A")
	chunk := make([]byte, 1<<20)
	for i := range chunk {
		chunk[i] = 'x'
	}
	total := 0
	for i := 0; i < 12; i++ {
		out, err := r.Write(chunk)
		if err != nil {
			t.Fatalf("Write error = %v", err)
		}
		for _, c := range out {
			total += len(c)
		}
	}
	if len(r.buf) > maxSSEBufferBytes {
		t.Fatalf("buffer grew to %d, want <= %d", len(r.buf), maxSSEBufferBytes)
	}
	if total == 0 {
		t.Fatal("nothing was released after exceeding the cap")
	}
}

// Audit #2: a trailing YAML comment must not flip enabled to false.
func TestDecodeLifecycleConfigIgnoresTrailingComments(t *testing.T) {
	cfg := decodeLifecycle(t, "enabled: true # 开关\nglobal_rules: gpt-4=>claude-opus-4-5 # 主映射\n")
	if !cfg.Enabled {
		t.Fatal("enabled=false, want true (trailing comment must not disable the plugin)")
	}
	if cfg.GlobalRules != "gpt-4=>claude-opus-4-5" {
		t.Fatalf("global_rules=%q, want the comment stripped", cfg.GlobalRules)
	}
}

// Audit #3: a quoted rule with a trailing comment must still parse.
func TestDecodeLifecycleConfigAcceptsQuotedRuleWithComment(t *testing.T) {
	cfg := decodeLifecycle(t, "enabled: true\nglobal_rules: \"gpt-4=>claude-opus-4-5\" # 主映射\n")
	if cfg.GlobalRules != "gpt-4=>claude-opus-4-5" {
		t.Fatalf("global_rules=%q", cfg.GlobalRules)
	}
}

// Audit #4: keys nested under another block must not clobber top-level keys.
func TestDecodeLifecycleConfigIgnoresNestedKeys(t *testing.T) {
	cfg := decodeLifecycle(t,
		"enabled: true\nglobal_rules: gpt-4=>claude-opus\nstore:\n    version: 1.2.3\n    enabled: false\npriority: 0\n")
	if !cfg.Enabled {
		t.Fatal("enabled=false, want true (store.enabled must not clobber the top-level key)")
	}
	if cfg.GlobalRules != "gpt-4=>claude-opus" {
		t.Fatalf("global_rules=%q", cfg.GlobalRules)
	}
}

// An omitted `enabled` keeps defaultConfig()'s true; an explicit false disables.
func TestDecodeLifecycleConfigDistinguishesAbsentFromFalse(t *testing.T) {
	if cfg := decodeLifecycle(t, "global_rules: a=>b\n"); !cfg.Enabled {
		t.Fatal("absent enabled must keep the default true")
	}
	if cfg := decodeLifecycle(t, "enabled: false\nglobal_rules: a=>b\n"); cfg.Enabled {
		t.Fatal("explicit enabled:false must disable")
	}
}

// A YAML double-quoted "\a" is the BEL escape, not the case operator. Report
// that clearly instead of a bare "invalid rule".
func TestParseRulesExplainsControlCharacters(t *testing.T) {
	_, err := parseRules("\x07;a=>b")
	if err == nil || !strings.Contains(err.Error(), "single quotes") {
		t.Fatalf("error = %v, want a hint about YAML single quotes", err)
	}
}

// Audit #6: reconfigure must swap state atomically — no window where the key
// bindings are missing.
func TestReconfigureNeverExposesEmptyState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	seed := State{
		Version:     stateVersion,
		Rules:       RuleSet{Global: "a=>b"},
		KeyBindings: []KeyBinding{{Key: "sk-live", Enabled: true, Rules: RuleSet{Global: "b=>c"}}},
	}
	if err := atomicWriteState(statePath, seed); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	raw := lifecycleRaw(t, "enabled: true\nglobal_rules: a=>b\nstate_file: "+statePath+"\n")
	if _, err := handlePluginReconfigure(raw); err != nil {
		t.Fatalf("initial reconfigure: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	emptyReads := 0
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if len(loadedRuleSource().KeyBindings) == 0 {
					mu.Lock()
					emptyReads++
					mu.Unlock()
					return
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		if _, err := handlePluginReconfigure(raw); err != nil {
			t.Fatalf("reconfigure %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if emptyReads != 0 {
		t.Fatalf("%d concurrent reads saw zero key bindings during reconfigure", emptyReads)
	}
}

// Audit #7: an unusable state_file is moved aside (so its bindings survive) and
// the reason is reported instead of silently dropped.
func TestResolveStateQuarantinesInvalidStateFile(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	corrupt := `{"version":1,"rules":{"global":"bad rule with spaces"},` +
		`"key_bindings":[{"key":"sk-important","enabled":true,"rules":{"global":"x=>y"}}]}`
	if err := os.WriteFile(statePath, []byte(corrupt), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	holder := resolveState(Config{Enabled: true, GlobalRules: "seed=>fromyaml", StateFile: statePath})
	if holder.loadError == "" {
		t.Fatal("loadError is empty, want the rejection reason")
	}
	if holder.persisted {
		t.Fatal("persisted=true, want the YAML seed fallback")
	}
	if _, err := os.Stat(statePath + ".corrupt"); err != nil {
		t.Fatalf("original state was not preserved: %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("rejected state file still in place: %v", err)
	}
}

// A missing state_file is the normal first run, not an error worth surfacing.
func TestResolveStateMissingFileReportsNoLoadError(t *testing.T) {
	holder := resolveState(Config{
		Enabled: true, GlobalRules: "a=>b",
		StateFile: filepath.Join(t.TempDir(), "absent.json"),
	})
	if holder.loadError != "" {
		t.Fatalf("loadError=%q, want empty for a missing file", holder.loadError)
	}
}

// Audit #10: RFC 7235 §2.1 makes the auth-scheme case-insensitive.
func TestAPIKeyFromHeadersAcceptsAnyBearerCase(t *testing.T) {
	for _, scheme := range []string{"Bearer", "bearer", "BEARER", "BeArEr"} {
		if got := apiKeyFromHeaders(http.Header{"Authorization": {scheme + " sk-abc"}}); got != "sk-abc" {
			t.Fatalf("scheme %q produced %q, want sk-abc", scheme, got)
		}
	}
	if got := apiKeyFromHeaders(http.Header{"Authorization": {"Basic Zm9v"}, "X-Api-Key": {"sk-x"}}); got != "sk-x" {
		t.Fatalf("non-Bearer scheme fell through to %q, want sk-x", got)
	}
}

// Audit #11: disk failures are 500, even when the OS message says "invalid".
func TestManagementStateErrorClassifiesPersistFailureAs500(t *testing.T) {
	err := &statePersistError{err: &osLikeError{msg: "rename /x/state.json: invalid argument"}}
	if resp := managementStateError(err); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", resp.StatusCode)
	}
}

type osLikeError struct{ msg string }

func (e *osLikeError) Error() string { return e.msg }

// Audit #16: a key with surrounding whitespace is stored trimmed, so the
// management routes (which look it up trimmed) can still reach it.
func TestManagementPostKeyTrimsKey(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	resp := managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"  sk-pad  ","alias":"A","enabled":true,"rules":{"global":"x=>y"}}`),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post status=%d body=%s", resp.StatusCode, resp.Body)
	}
	del := managementDeleteKey(pluginapi.ManagementRequest{
		Method: http.MethodDelete,
		Query:  url.Values{"key": {"sk-pad"}},
	})
	if del.StatusCode != http.StatusOK {
		t.Fatalf("delete status=%d body=%s, want the trimmed key to be reachable", del.StatusCode, del.Body)
	}
}

// Audit #22: only the registered resource path serves the admin page.
func TestDispatchManagementResourceRouteIsExact(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	ok := dispatchManagement(pluginapi.ManagementRequest{
		Method: http.MethodGet, Path: resourcePrefix + "/index.html",
	})
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("index.html status=%d, want 200", ok.StatusCode)
	}
	for _, path := range []string{resourcePrefix + "-evil/index.html", resourcePrefix + "/other.html"} {
		resp := dispatchManagement(pluginapi.ManagementRequest{Method: http.MethodGet, Path: path})
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status=%d, want 404", path, resp.StatusCode)
		}
	}
}

// Audit #17: a rules change between routing and execution degrades to passing
// the client's model through, not to a failed in-flight request.
func TestUpstreamModelForPassesThroughWhenNoLongerRouted(t *testing.T) {
	setLoadedConfigForTest(Config{Enabled: true, GlobalRules: "other=>mapped"})
	upstream, original := upstreamModelFor("openai", "unmapped-model", http.Header{})
	if upstream != "unmapped-model" || original != "unmapped-model" {
		t.Fatalf("upstream=%q original=%q, want the request model passed through", upstream, original)
	}
}

// Audit #8: a panic inside a plugin method is converted to an error instead of
// aborting the host process.
func TestSafeCallRecoversPanic(t *testing.T) {
	resp, err := safeCall(func() ([]byte, error) { panic("boom") })
	if err == nil || !strings.Contains(err.Error(), "panic recovered") {
		t.Fatalf("err=%v, want a recovered panic", err)
	}
	if resp != nil {
		t.Fatalf("resp=%q, want nil", resp)
	}
}

// Audit #15: a rule whose captures collapse to an empty name is skipped, and
// the model reaches the upstream unchanged rather than failing the request.
func TestApplyRulesSkipsEntryThatWouldBlankTheModel(t *testing.T) {
	mapped, matched, err := applyRules("-mini", mustParseRules(t, `*-mini=>$1`))
	if err != nil {
		t.Fatalf("applyRules error = %v, want the entry skipped", err)
	}
	if mapped != "-mini" || matched {
		t.Fatalf("mapped=%q matched=%v, want the model untouched", mapped, matched)
	}
}

// Backtracking must stay linear-ish: a pattern with several captures against a
// long model name should not blow up combinatorially.
func TestMatchTokensBacktrackingStaysBounded(t *testing.T) {
	rules := mustParseRules(t, `*a*a*a*a*b=>hit`)
	model := strings.Repeat("a", 512)
	mapped, matched, err := applyRules(model, rules)
	if err != nil {
		t.Fatalf("applyRules error = %v", err)
	}
	if matched || mapped != model {
		t.Fatalf("mapped=%q matched=%v, want no match", mapped, matched)
	}
}
