package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"unicode"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"gopkg.in/yaml.v3"
)

func main() {}

// logger writes to stderr, which the CPA host inherits. Without it every
// config/state/routing failure in this plugin is silent (audit finding #9).
var logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})).
	With("plugin", "model-mapper-plus")

var pluginVersion = "0.0.0-dev.unbuilt"

type sseRewriter struct {
	originalModel string
	buf           []byte
	// overflowed marks that buf exceeded maxSSEBufferBytes and was released
	// unrewritten; further bytes stream through untouched rather than growing
	// the buffer without bound.
	overflowed bool
}

// maxSSEBufferBytes caps how much of a single unterminated SSE event we hold.
// An upstream that never emits a blank line would otherwise grow buf forever.
const maxSSEBufferBytes = 8 << 20

type streamChunkRewriter struct {
	originalModel     string
	frameRawJSONAsSSE bool
	sse               *sseRewriter
	pending           []byte
	// sseMode latches once the stream is known to be SSE. Classification is
	// per-chunk but the SSE buffer is stateful, so a mid-event chunk that does
	// not itself look like SSE must still be appended to the buffer instead of
	// bypassing it (which reordered and un-rewrote output).
	sseMode bool
}

func newSSERewriter(originalModel string) *sseRewriter {
	return &sseRewriter{originalModel: originalModel}
}

func newStreamChunkRewriter(originalModel string) *streamChunkRewriter {
	return &streamChunkRewriter{
		originalModel: originalModel,
		sse:           newSSERewriter(originalModel),
	}
}

func (r *sseRewriter) Write(p []byte) ([][]byte, error) {
	if r.overflowed {
		return [][]byte{append([]byte(nil), p...)}, nil
	}
	if sseNeedsLineBreak(r.buf, p) {
		r.buf = append(r.buf, '\n')
	}
	r.buf = append(r.buf, p...)
	var out [][]byte
	for {
		delim, n := sseEventDelimiter(r.buf)
		if n == 0 {
			break
		}
		event := append([]byte(nil), r.buf[:delim]...)
		r.buf = r.buf[delim+n:]
		rewritten, err := r.rewriteEvent(event)
		if err != nil {
			return nil, err
		}
		// One emit per event, not per line: each emit is a full C-ABI round
		// trip, and a long stream otherwise costs 3+ crossings per event.
		out = append(out, joinChunks(rewritten, r.delimiterBytes(n)))
	}
	if len(r.buf) > maxSSEBufferBytes {
		logger.Warn("sse buffer exceeded cap, passing through unrewritten",
			"bytes", len(r.buf), "cap", maxSSEBufferBytes)
		r.overflowed = true
		out = append(out, append([]byte(nil), r.buf...))
		r.buf = nil
	}
	return out, nil
}

// joinChunks concatenates rewritten lines plus the event delimiter into one buffer.
func joinChunks(parts [][]byte, delim []byte) []byte {
	size := len(delim)
	for _, part := range parts {
		size += len(part)
	}
	out := make([]byte, 0, size)
	for _, part := range parts {
		out = append(out, part...)
	}
	return append(out, delim...)
}

func (r *sseRewriter) Flush() ([][]byte, error) {
	if len(r.buf) == 0 {
		return nil, nil
	}
	event := append([]byte(nil), r.buf...)
	r.buf = nil
	if r.overflowed {
		return [][]byte{event}, nil
	}
	return r.rewriteEvent(event)
}

func (r *sseRewriter) rewriteEvent(event []byte) ([][]byte, error) {
	var out [][]byte
	for len(event) > 0 {
		lineEnd := bytes.IndexByte(event, '\n')
		line := event
		lineBreak := []byte(nil)
		if lineEnd >= 0 {
			line = event[:lineEnd]
			lineBreak = []byte("\n")
			event = event[lineEnd+1:]
		} else {
			event = nil
		}
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
			if len(lineBreak) > 0 {
				lineBreak = []byte("\r\n")
			}
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			value := bytes.TrimSpace(line[len("data:"):])
			if len(value) == 0 || bytes.Equal(value, []byte("[DONE]")) {
				out = append(out, append(append([]byte(nil), line...), lineBreak...))
				continue
			}
			restored, changed, err := restoreResponseModel(value, r.originalModel)
			if err != nil {
				return nil, err
			}
			if changed {
				out = append(out, append(append([]byte("data: "), restored...), lineBreak...))
				continue
			}
		}
		out = append(out, append(append([]byte(nil), line...), lineBreak...))
	}
	return out, nil
}

func sseEventDelimiter(buf []byte) (eventLen, delimLen int) {
	lf := bytes.Index(buf, []byte("\n\n"))
	crlf := bytes.Index(buf, []byte("\r\n\r\n"))
	if lf < 0 && crlf < 0 {
		return 0, 0
	}
	if lf >= 0 && (crlf < 0 || lf < crlf) {
		return lf, 2
	}
	return crlf, 4
}

func (r *sseRewriter) delimiterBytes(n int) []byte {
	if n == 4 {
		return []byte("\r\n\r\n")
	}
	return []byte("\n\n")
}

func sseNeedsLineBreak(pending, chunk []byte) bool {
	if len(pending) == 0 || len(chunk) == 0 {
		return false
	}
	if bytes.HasSuffix(pending, []byte("\n")) || bytes.HasSuffix(pending, []byte("\r")) {
		return false
	}
	if chunk[0] == '\n' || chunk[0] == '\r' {
		return false
	}
	trimmed := bytes.TrimLeft(chunk, " \t")
	for _, prefix := range [][]byte{[]byte("data:"), []byte("event:"), []byte("id:"), []byte("retry:"), []byte(":")} {
		if bytes.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func (r *streamChunkRewriter) Write(p []byte) ([][]byte, error) {
	if len(r.pending) > 0 {
		r.pending = append(r.pending, p...)
		p = append([]byte(nil), r.pending...)
		r.pending = nil
	}
	// Once SSE, always SSE — and while the SSE buffer still holds a partial
	// event, every subsequent byte belongs to it. Re-classifying per chunk let
	// a mid-event fragment bypass the buffer, emitting it ahead of the lines
	// already buffered and skipping model restoration on both.
	if r.sseMode || len(r.sse.buf) > 0 {
		r.sseMode = true
		return r.sse.Write(p)
	}
	if isSSEChunk(p) {
		r.sseMode = true
		return r.sse.Write(p)
	}
	if isIncompleteSSEPrefix(p) {
		r.pending = append(r.pending, p...)
		return nil, nil
	}
	restored, changed, err := restoreResponseModel(p, r.originalModel)
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), p...)
	if changed {
		out = restored
	}
	return r.rawJSONChunks(out)
}

func (r *streamChunkRewriter) rawJSONChunks(p []byte) ([][]byte, error) {
	values, ok := splitJSONValues(p)
	if !ok {
		if r.frameRawJSONAsSSE && json.Valid(p) {
			return [][]byte{frameSSEData(p)}, nil
		}
		return [][]byte{append([]byte(nil), p...)}, nil
	}
	if len(values) == 0 {
		return nil, nil
	}
	out := make([][]byte, 0, len(values))
	for _, value := range values {
		restored, _, err := restoreResponseModel(value, r.originalModel)
		if err != nil {
			return nil, err
		}
		if r.frameRawJSONAsSSE {
			out = append(out, frameSSEData(restored))
			continue
		}
		out = append(out, restored)
	}
	return out, nil
}

func splitJSONValues(p []byte) ([][]byte, bool) {
	if len(bytes.TrimSpace(p)) == 0 {
		return nil, true
	}
	dec := json.NewDecoder(bytes.NewReader(p))
	values := make([][]byte, 0, 1)
	for {
		var raw json.RawMessage
		err := dec.Decode(&raw)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
		values = append(values, append([]byte(nil), raw...))
	}
	return values, len(values) > 0
}

func (r *streamChunkRewriter) Flush() ([][]byte, error) {
	if len(r.pending) > 0 {
		pending := append([]byte(nil), r.pending...)
		r.pending = nil
		chunks, err := r.sse.Write(pending)
		if err != nil {
			return nil, err
		}
		flushed, err := r.sse.Flush()
		if err != nil {
			return nil, err
		}
		return append(chunks, flushed...), nil
	}
	return r.sse.Flush()
}

func frameSSEData(p []byte) []byte {
	var out bytes.Buffer
	for _, line := range bytes.Split(p, []byte("\n")) {
		out.WriteString("data: ")
		out.Write(line)
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	return out.Bytes()
}

func isSSEChunk(p []byte) bool {
	trimmed := bytes.TrimLeft(p, " \t\r\n")
	if bytes.HasPrefix(trimmed, []byte("data:")) || bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte(":")) {
		return true
	}
	return bytes.Contains(p, []byte("\n\n")) || bytes.Contains(p, []byte("\r\n\r\n"))
}

func isIncompleteSSEPrefix(p []byte) bool {
	trimmed := bytes.TrimLeft(p, " \t\r\n")
	if len(trimmed) == 0 || strings.ContainsAny(string(trimmed), "\r\n") {
		return false
	}
	for _, field := range [][]byte{[]byte("data:"), []byte("event:"), []byte(":")} {
		if bytes.HasPrefix(field, trimmed) && len(trimmed) < len(field) {
			return true
		}
	}
	return false
}

type Config struct {
	UsageKeeperURL         string `json:"usage_keeper_url"`
	UsageKeeperPasswordEnv string `json:"usage_keeper_password_env"`
	Enabled                bool   `json:"enabled"`
	GlobalRules            string `json:"global_rules"`
	ClaudeMessagesRules    string `json:"claude_messages_rules"`
	CodexResponsesRules    string `json:"codex_responses_rules"`
	OpenAICompletionsRules string `json:"openai_completions_rules"`
	StateFile              string `json:"state_file"`
}

type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginapi.Metadata       `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	ModelRouter           bool     `json:"model_router"`
	Executor              bool     `json:"executor"`
	ExecutorModelScope    string   `json:"executor_model_scope"`
	ExecutorInputFormats  []string `json:"executor_input_formats"`
	ExecutorOutputFormats []string `json:"executor_output_formats"`
	ManagementAPI         bool     `json:"management_api"`
	RequestInterceptor    bool     `json:"request_interceptor"`
	Scheduler             bool     `json:"scheduler"`
	ResponseInterceptor   bool     `json:"response_interceptor"`
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "model-mapper-plus",
			Version:          pluginVersion,
			Author:           "FlameMida",
			GitHubRepository: "https://github.com/FlameMida/cpa-model-mapper-plus",
			// 声明宿主配置页维护的开关、state 路径和 Keeper 接入字段。规则四段（global_rules /
			// claude_messages_rules / codex_responses_rules / openai_completions_rules）
			// 仍能从 YAML 读入作为首次 seed，但由插件自己的管理页维护，不在 CPA 页面重复暴露。
			//
			// 文案里不要出现 & ' < > "：CPA 渲染插件元数据时对 Name/Type/EnumValues/
			// Description 逐个跑 html.EscapeString（pluginConfigFields），这五个字符会变成
			// &lt; &gt; 之类的实体、在配置页上显示为乱码，且插件侧无法关闭宿主的转义。
			ConfigFields: []pluginapi.ConfigField{
				{Name: "enabled", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Enable model request mapping."},
				{Name: "state_file", Type: pluginapi.ConfigFieldTypeString, Description: "Path to the JSON state file holding mapping rules and key bindings. Relative paths resolve against the CPA process working directory; leave empty to use model-mapper-plus-state.json there. Edit the rules themselves in the Model Mapper Plus admin page."},
				{Name: "usage_keeper_url", Type: pluginapi.ConfigFieldTypeString, Description: "Keeper base URL reachable from the CPA process, including any deployment subpath. Leave empty to disable API key alias lookup."},
				{Name: "usage_keeper_password_env", Type: pluginapi.ConfigFieldTypeString, Description: "Environment variable holding the Keeper administrator login password. Defaults to CPA_KEEPER_LOGIN_PASSWORD. Set the actual password in the CPA process environment and restart CPA after changing it."},
			},
		},
		Capabilities: registrationCapabilities{
			ModelRouter:           true,
			Executor:              true,
			ExecutorModelScope:    string(pluginapi.ExecutorModelScopeStatic),
			ExecutorInputFormats:  []string{"openai", "claude", "openai-response"},
			ExecutorOutputFormats: []string{"openai", "claude", "openai-response"},
			ManagementAPI:         true,
			RequestInterceptor:    true,
			Scheduler:             true,
			ResponseInterceptor:   true,
		},
	}
}

type channelRoundRobinState struct {
	signature string
	next      uint64
}

type channelRoundRobin struct {
	mu     sync.Mutex
	states map[string]channelRoundRobinState
}

var channelTargetRoundRobin = channelRoundRobin{states: make(map[string]channelRoundRobinState)}

func (r *channelRoundRobin) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = make(map[string]channelRoundRobinState)
}

func (r *channelRoundRobin) pick(key string, candidates []pluginapi.SchedulerAuthCandidate) string {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
	}
	sort.Strings(ids)
	signature := strings.Join(ids, "\x00")

	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.states[key]
	if state.signature != signature {
		state = channelRoundRobinState{signature: signature}
	}
	id := ids[state.next%uint64(len(ids))]
	state.next++
	r.states[key] = state
	return id
}

func decodeConfig(raw json.RawMessage) (Config, error) {
	cfg := defaultConfig()
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("{}")) {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(cfg.UsageKeeperPasswordEnv) == "" {
		cfg.UsageKeeperPasswordEnv = defaultConfig().UsageKeeperPasswordEnv
	}
	for _, rules := range []string{cfg.GlobalRules, cfg.ClaudeMessagesRules, cfg.CodexResponsesRules, cfg.OpenAICompletionsRules} {
		if rules == "" {
			continue
		}
		if _, err := parseRules(rules); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

var (
	loadedConfigMu sync.RWMutex
	loadedCfg      = defaultConfig()

	hostAPIMu      sync.RWMutex
	hostCallbackFn hostCallback

	loadedStateMu sync.RWMutex
	loadedHolder  = stateHolder{src: ruleSourceFromConfig(defaultConfig())}
)

type stateHolder struct {
	src       ruleSource
	persisted bool
	state     State
	// loadError records why a present state_file could not be used, so the
	// fallback to the YAML seed is visible in GET /state instead of silent.
	loadError string
}

func loadedRuleSource() ruleSource {
	loadedStateMu.RLock()
	defer loadedStateMu.RUnlock()
	return cloneRuleSource(loadedHolder.src)
}

func loadedStateSnapshot() (State, bool) {
	loadedStateMu.RLock()
	defer loadedStateMu.RUnlock()
	return cloneState(loadedHolder.state), loadedHolder.persisted
}

// loadedStateLoadError reports why the on-disk state_file was rejected, if any.
func loadedStateLoadError() string {
	loadedStateMu.RLock()
	defer loadedStateMu.RUnlock()
	return loadedHolder.loadError
}

func keyBindingExists(key string) bool {
	loadedStateMu.RLock()
	defer loadedStateMu.RUnlock()
	for _, b := range loadedHolder.state.KeyBindings {
		if b.Key == key {
			return true
		}
	}
	return false
}

// resolveState loads state_file when present and valid; otherwise falls back
// to a YAML-seed state without creating the file (ADR-0001). A state file that
// exists but fails validation is renamed aside before the fallback, so the next
// management save cannot silently overwrite (and destroy) its key bindings.
func resolveState(cfg Config) stateHolder {
	seedHolder := func(loadErr string) stateHolder {
		seed := seedStateFromConfig(cfg)
		return stateHolder{src: ruleSourceFromState(seed), state: seed, loadError: loadErr}
	}
	path, err := ResolveStatePath(cfg.StateFile)
	if err != nil {
		return seedHolder(fmt.Sprintf("resolve state path: %v", err))
	}
	st, err := readStateFile(path)
	if err == nil {
		if err = validateState(st); err == nil {
			return stateHolder{src: ruleSourceFromState(st), persisted: true, state: st}
		}
		// Invalid content: preserve the file so its key bindings stay recoverable.
		quarantine := path + ".corrupt"
		if renameErr := os.Rename(path, quarantine); renameErr == nil {
			return seedHolder(fmt.Sprintf("%v (moved aside to %s)", err, quarantine))
		}
		return seedHolder(err.Error())
	}
	if os.IsNotExist(err) {
		// First run: YAML seed is the source of truth until the first save.
		return seedHolder("")
	}
	return seedHolder(err.Error())
}

func stateFilePathFrom(cfg Config) string {
	path, err := ResolveStatePath(cfg.StateFile)
	if err != nil {
		// Fall back to default basename if Abs fails (should be rare).
		return defaultStateFile
	}
	return path
}

func stateFilePath() string {
	return stateFilePathFrom(loadedConfig())
}

// applyStateUpdate deep-clones the current state, applies mutate, validates,
// persists atomically, then swaps the runtime view. Any failure leaves the
// previous state untouched (H2). Path is resolved before taking loadedStateMu
// to avoid AB-BA lock order with reconfigure (H4).
func applyStateUpdate(mutate func(*State) error) error {
	path := stateFilePath()
	loadedStateMu.Lock()
	defer loadedStateMu.Unlock()
	st := cloneState(loadedHolder.state)
	if err := mutate(&st); err != nil {
		return err
	}
	if err := validateState(st); err != nil {
		return err
	}
	if err := atomicWriteState(path, st); err != nil {
		logger.Error("state persist failed", "state_file", path, "err", err)
		return &statePersistError{err: err}
	}
	// A successful write clears any prior load error: disk is authoritative again.
	loadedHolder = stateHolder{src: ruleSourceFromState(st), persisted: true, state: st}
	return nil
}

type hostCallback func(method string, request []byte) ([]byte, error)

func loadedConfig() Config {
	loadedConfigMu.RLock()
	defer loadedConfigMu.RUnlock()
	return loadedCfg
}

func setLoadedConfigForTest(cfg Config) {
	loadedConfigMu.Lock()
	loadedCfg = cfg
	resetKeeperAliases()
	loadedConfigMu.Unlock()
	loadedStateMu.Lock()
	loadedHolder = stateHolder{src: ruleSourceFromConfig(cfg), state: seedStateFromConfig(cfg)}
	loadedStateMu.Unlock()
}

func handlePluginRegister(raw []byte) ([]byte, error) {
	// A newly loaded library receives its config in plugin.register; the host
	// only sends plugin.reconfigure on later reloads. Load state immediately.
	return handlePluginReconfigure(raw)
}

func handlePluginReconfigure(raw []byte) ([]byte, error) {
	cfgRaw, _, err := decodeLifecycleConfig(raw)
	if err != nil {
		logger.Error("reconfigure: decode lifecycle config failed", "err", err)
		return nil, err
	}
	cfg, err := decodeConfig(cfgRaw)
	if err != nil {
		logger.Error("reconfigure: invalid plugin config", "err", err)
		return nil, err
	}
	managementMutationMu.Lock()
	defer managementMutationMu.Unlock()
	// Resolve (and read from disk) outside config/state locks: doing it under the state
	// write lock blocked all readers, and the previous two-step swap left a
	// window where loadedHolder held a seed-only state with no key bindings —
	// concurrent routes lost the key layer and a concurrent save persisted the
	// empty bindings to disk.
	// Snapshot the previously loaded state before resolving the new path. A
	// path change to a nonexistent file migrates the current data instead of
	// dropping it (ADR-0001 refinement: first run, which has no persisted
	// data, still falls back to the YAML seed and creates nothing).
	oldState, oldPersisted := loadedStateSnapshot()

	holder := resolveState(cfg)

	// state_file switched to a path that does not exist yet, but we already
	// have persisted data on the old path: write the current data to the new
	// path so reconfiguring to a new location does not look like the rules and
	// key bindings vanished. A corrupt new path (loadError != "") keeps the
	// quarantine-and-seed path; only the "does not exist" case migrates.
	if !holder.persisted && holder.loadError == "" && oldPersisted {
		newPath := stateFilePathFrom(cfg)
		if err := atomicWriteState(newPath, oldState); err != nil {
			logger.Warn("reconfigure: migrate state to new path failed",
				"to", newPath, "err", err)
		} else {
			logger.Info("reconfigure: migrated persisted state to new state_file", "to", newPath)
			holder = resolveState(cfg) // re-resolve; the new path now exists
		}
	}

	if holder.loadError != "" {
		logger.Warn("reconfigure: state file unusable, falling back to YAML seed",
			"state_file", stateFilePathFrom(cfg), "err", holder.loadError)
	}
	loadedConfigMu.Lock()
	loadedCfg = cfg
	resetKeeperAliases()
	loadedConfigMu.Unlock()
	loadedStateMu.Lock()
	loadedHolder = holder
	loadedStateMu.Unlock()
	restartNotificationServiceWithState(holder.state)
	logger.Info("reconfigure applied", "enabled", cfg.Enabled,
		"persisted", holder.persisted, "key_bindings", len(holder.state.KeyBindings))
	return json.Marshal(pluginRegistration())
}

func handleExecutorIdentifier() ([]byte, error) {
	return json.Marshal(struct {
		Identifier string `json:"identifier"`
	}{Identifier: "model-mapper-plus"})
}

type routeDecision struct {
	Handled       bool
	OriginalModel string
	UpstreamModel string
}

func selectRules(cfg Config, format string) (string, bool) {
	return selectRulesFrom(ruleSetFromConfig(cfg), format)
}

// apiKeyFromHeaders extracts the client API key from inbound headers:
// "Authorization: Bearer <key>" wins, then "x-api-key" (ADR key extraction).
// The auth-scheme match is case-insensitive per RFC 7235 §2.1 — a client
// sending "bearer sk-…" must still reach its key binding.
func apiKeyFromHeaders(h http.Header) string {
	if auth := strings.TrimSpace(h.Get("Authorization")); auth != "" {
		if scheme, token, ok := strings.Cut(auth, " "); ok && strings.EqualFold(scheme, "Bearer") {
			if token = strings.TrimSpace(token); token != "" {
				return token
			}
		}
	}
	return strings.TrimSpace(h.Get("x-api-key"))
}

const blockedQuotaExhaustedBody = `{"error":{"message":"Your quota has been exhausted.","type":"permission_error","code":"insufficient_quota"}}`

const fastModeBeta = "fast-mode-2026-02-01"

func stripFastBeta(headers http.Header) (http.Header, []string) {
	values := headers.Values("Anthropic-Beta")
	if len(values) == 0 {
		return nil, nil
	}
	kept := make([]string, 0)
	found := false
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			if token == fastModeBeta {
				found = true
				continue
			}
			kept = append(kept, token)
		}
	}
	if !found {
		return nil, nil
	}
	if len(kept) == 0 {
		return nil, []string{"Anthropic-Beta"}
	}
	return http.Header{"Anthropic-Beta": {strings.Join(kept, ",")}}, nil
}

func stripFastBody(body []byte) ([]byte, bool, error) {
	value := gjson.GetBytes(body, "speed")
	if !value.Exists() || !strings.EqualFold(value.String(), "fast") {
		return nil, false, nil
	}
	out, err := sjson.DeleteBytes(body, "speed")
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func handleRequestInterceptBefore(raw []byte) ([]byte, error) {
	return handleRequestInterceptBeforeWithRuleSource(raw, loadedRuleSource)
}

func handleRequestInterceptBeforeWithRuleSource(raw []byte, load func() ruleSource) ([]byte, error) {
	var req pluginapi.RequestInterceptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if !loadedConfig().Enabled {
		return json.Marshal(pluginapi.RequestInterceptResponse{})
	}
	src := load()
	apiKey := apiKeyFromHeaders(req.Headers)
	if _, blocked := findBlockedKeyBinding(src.KeyBindings, apiKey); blocked {
		return json.Marshal(pluginapi.RequestInterceptResponse{
			Terminate:       true,
			StatusCode:      http.StatusForbidden,
			ResponseHeaders: http.Header{"Content-Type": {"application/json"}},
			ResponseBody:    []byte(blockedQuotaExhaustedBody),
		})
	}
	binding, exists := findKeyBindingByKey(src.KeyBindings, apiKey)
	if !exists || binding.FastAllowed == nil || *binding.FastAllowed || !strings.EqualFold(req.SourceFormat, "claude") {
		return json.Marshal(pluginapi.RequestInterceptResponse{})
	}
	body, bodyChanged, err := stripFastBody(req.Body)
	if err != nil {
		return nil, err
	}
	headers, clearHeaders := stripFastBeta(req.Headers)
	resp := pluginapi.RequestInterceptResponse{Headers: headers, ClearHeaders: clearHeaders}
	if bodyChanged {
		resp.Body = body
	}
	return json.Marshal(resp)
}

func handleRequestInterceptAfter(raw []byte) ([]byte, error) {
	// Required by RequestInterceptor capability; access gate runs only before auth.
	_ = raw
	return json.Marshal(pluginapi.RequestInterceptResponse{})
}

var unavailableSchedulerStatuses = map[string]struct{}{
	"disabled": {}, "expired": {}, "revoked": {}, "invalid": {},
	"unavailable": {}, "cooldown": {}, "cooling_down": {},
	"quota_exhausted": {}, "exhausted": {}, "blocked": {},
}

func schedulerCandidateUsable(candidate pluginapi.SchedulerAuthCandidate) bool {
	// The host already checks model-specific availability and cooldowns.
	// Status "error" may describe a past failure of this or another model.
	_, unavailable := unavailableSchedulerStatuses[strings.ToLower(strings.TrimSpace(candidate.Status))]
	return strings.TrimSpace(candidate.ID) != "" && !unavailable
}

func schedulerCandidateTargeted(candidate pluginapi.SchedulerAuthCandidate, target *ChannelTarget) bool {
	for _, id := range target.AuthIDs {
		if strings.TrimSpace(id) == strings.TrimSpace(candidate.ID) {
			return true
		}
	}
	for _, supplier := range target.Suppliers {
		if strings.EqualFold(strings.TrimSpace(supplier), strings.TrimSpace(candidate.Provider)) {
			return true
		}
	}
	return false
}

const channelTargetAuthNotFoundMessage = `{"error":{"type":"auth_not_found","code":"auth_not_found","message":"no usable auth candidate in channel target"}}`

type pluginMethodError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *pluginMethodError) Error() string { return e.Message }

func handleSchedulerPick(raw []byte) ([]byte, error) {
	var req pluginapi.SchedulerPickRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if !loadedConfig().Enabled {
		return json.Marshal(pluginapi.SchedulerPickResponse{Handled: false})
	}
	apiKey := apiKeyFromHeaders(http.Header(req.Options.Headers))
	binding, targeted := findActiveChannelTarget(loadedRuleSource().KeyBindings, apiKey)
	if !targeted {
		return json.Marshal(pluginapi.SchedulerPickResponse{Handled: false})
	}
	pool := make([]pluginapi.SchedulerAuthCandidate, 0, len(req.Candidates))
	for _, candidate := range req.Candidates {
		if schedulerCandidateUsable(candidate) && schedulerCandidateTargeted(candidate, binding.ChannelTarget) {
			pool = append(pool, candidate)
		}
	}
	if len(pool) == 0 {
		return nil, &pluginMethodError{
			Code:       "auth_not_found",
			Message:    channelTargetAuthNotFoundMessage,
			HTTPStatus: http.StatusServiceUnavailable,
		}
	}
	return json.Marshal(pluginapi.SchedulerPickResponse{
		Handled: true,
		AuthID:  channelTargetRoundRobin.pick(binding.Key, pool),
	})
}

func handleResponseInterceptAfter(raw []byte) ([]byte, error) {
	var req pluginapi.ResponseInterceptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if !loadedConfig().Enabled || req.Stream || strings.TrimSpace(req.RequestedModel) == "" {
		return json.Marshal(pluginapi.ResponseInterceptResponse{})
	}
	apiKey := apiKeyFromHeaders(req.RequestHeaders)
	if _, targeted := findActiveChannelTarget(loadedRuleSource().KeyBindings, apiKey); !targeted {
		return json.Marshal(pluginapi.ResponseInterceptResponse{})
	}
	body, changed, err := rewriteResponseModelFields(req.Body, req.RequestedModel)
	if err != nil {
		return nil, err
	}
	if !changed {
		return json.Marshal(pluginapi.ResponseInterceptResponse{})
	}
	return json.Marshal(pluginapi.ResponseInterceptResponse{Body: body})
}

func selectRulesFrom(rs RuleSet, format string) (string, bool) {
	switch format {
	case "claude":
		if rs.Claude != "" {
			return rs.Claude, true
		}
	case "openai-response":
		if rs.Codex != "" {
			return rs.Codex, true
		}
	case "openai":
		if rs.OpenAI != "" {
			return rs.OpenAI, true
		}
	}
	if rs.Global != "" {
		return rs.Global, true
	}
	return "", false
}

// applyRuleSet selects the segment for format and applies it once.
// It returns (output, matched, error); unmatched leaves model unchanged.
func applyRuleSet(rs RuleSet, format, model string) (string, bool, error) {
	raw, ok := selectRulesFrom(rs, format)
	if !ok {
		return model, false, nil
	}
	rules, err := parseRules(raw)
	if err != nil {
		return "", false, err
	}
	mapped, matched, err := applyRules(model, rules)
	if err != nil {
		return "", false, err
	}
	if !matched {
		return model, false, nil
	}
	return mapped, true, nil
}

func handleModelRoute(raw []byte) ([]byte, error) {
	var req pluginapi.ModelRouteRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	decision, err := routeModelForRequest(loadedConfig(), loadedRuleSource(), req.SourceFormat, req.RequestedModel, apiKeyFromHeaders(req.Headers), req.Body)
	if err != nil {
		return nil, err
	}
	if !decision.Handled {
		return json.Marshal(pluginapi.ModelRouteResponse{Handled: false})
	}
	return json.Marshal(pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetSelf, Reason: "model mapped by model-mapper-plus"})
}

func routeModel(cfg Config, src ruleSource, format, model, apiKey string) (routeDecision, error) {
	if !cfg.Enabled {
		return routeDecision{}, nil
	}
	if _, targeted := findActiveChannelTarget(src.KeyBindings, apiKey); targeted {
		return routeDecision{}, nil
	}
	current := model
	mapped, matched, err := applyRuleSet(src.Rules, format, current)
	if err != nil {
		return routeDecision{}, err
	}
	if matched {
		current = mapped
	}
	if binding, ok := findKeyBinding(src.KeyBindings, apiKey); ok {
		mapped, matched, err := applyRuleSet(binding.Rules, format, current)
		if err != nil {
			return routeDecision{}, err
		}
		if matched {
			current = mapped
		}
	}
	if current == model {
		return routeDecision{}, nil
	}
	return routeDecision{Handled: true, OriginalModel: model, UpstreamModel: current}, nil
}

// routeModelForRequest makes the discrete reasoning effort in a Codex Responses
// body visible to the suffix-based mapping DSL. An explicit model suffix keeps
// CPA's normal priority over body fields. If the effort-qualified model does not
// match, retry the original model so existing mappings continue to work.
func routeModelForRequest(cfg Config, src ruleSource, format, model, apiKey string, body []byte) (routeDecision, error) {
	effort := codexReasoningEffort(format, model, body)
	if effort != "" {
		qualifiedModel := model + "(" + effort + ")"
		decision, err := routeModel(cfg, src, format, qualifiedModel, apiKey)
		if err != nil {
			return routeDecision{}, err
		}
		if decision.Handled {
			decision.OriginalModel = model
			return decision, nil
		}
	}
	return routeModel(cfg, src, format, model, apiKey)
}

func codexReasoningEffort(format, model string, body []byte) string {
	if format != "openai-response" || hasModelSuffix(model) || len(body) == 0 {
		return ""
	}
	var payload struct {
		Reasoning struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	effort := strings.ToLower(strings.TrimSpace(payload.Reasoning.Effort))
	switch effort {
	case "none", "auto", "minimal", "low", "medium", "high", "xhigh", "max":
		return effort
	default:
		return ""
	}
}

func hasModelSuffix(model string) bool {
	open := strings.LastIndexByte(model, '(')
	return open > 0 && strings.HasSuffix(model, ")") && strings.TrimSpace(model[open+1:len(model)-1]) != ""
}

func rewriteRequestModel(body []byte, upstreamModel string) ([]byte, bool, error) {
	return rewriteTopLevelModel(body, upstreamModel)
}

func restoreResponseModel(body []byte, originalModel string) ([]byte, bool, error) {
	return rewriteResponseModelFields(body, originalModel)
}

type hostCaller func(method string, payload any) (json.RawMessage, error)

type executorRPCRequest struct {
	pluginapi.ExecutorRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
	StreamID       string `json:"stream_id,omitempty"`
}

type hostModelExecutePayload struct {
	pluginapi.HostModelExecutionRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func handleExecutorExecuteStream(raw []byte, call hostCaller) ([]byte, error) {
	var req executorRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if req.StreamID == "" {
		return nil, fmt.Errorf("missing plugin stream id")
	}
	return startExecutorStream(req, call, func(streamID, errText string) error {
		_, err := call(pluginabi.MethodHostStreamClose, struct {
			StreamID string `json:"stream_id"`
			Error    string `json:"error,omitempty"`
		}{StreamID: streamID, Error: errText})
		return err
	})
}

func startExecutorStream(req executorRPCRequest, call hostCaller, closeStream func(string, string) error) ([]byte, error) {
	go func() {
		// This goroutine has no caller to recover for it; an unrecovered panic
		// here would abort the whole CPA process.
		defer func() {
			if r := recover(); r != nil {
				logger.Error("stream forward panic recovered", "panic", r, "model", req.Model)
				_ = closeStream(req.StreamID, fmt.Sprintf("plugin panic recovered: %v", r))
			}
		}()
		if err := runStreamForward(req, call); err != nil {
			logger.Error("stream forward failed", "model", req.Model, "err", err)
			_ = closeStream(req.StreamID, err.Error())
		}
	}()
	return json.Marshal(map[string]any{"headers": http.Header{"Content-Type": []string{"text/event-stream"}}})
}

// upstreamModelFor resolves the outbound model for an executor call. Routing is
// re-evaluated at execution time, so a rules edit between route and execute can
// make the decision unhandled; that must degrade to passing the client's model
// through, not fail an in-flight request.
func upstreamModelFor(sourceFormat, model string, headers http.Header) (upstream, original string) {
	return upstreamModelForRequest(sourceFormat, model, headers, nil)
}

func upstreamModelForRequest(sourceFormat, model string, headers http.Header, body []byte) (upstream, original string) {
	decision, err := routeModelForRequest(loadedConfig(), loadedRuleSource(), sourceFormat, model, apiKeyFromHeaders(headers), body)
	if err != nil {
		logger.Warn("route failed at execute time, passing model through", "model", model, "err", err)
		return model, model
	}
	if !decision.Handled {
		logger.Info("route no longer maps this model, passing through", "model", model)
		return model, model
	}
	return decision.UpstreamModel, decision.OriginalModel
}

func runStreamForward(req executorRPCRequest, call hostCaller) error {
	upstreamModel, originalModel := upstreamModelForRequest(req.SourceFormat, req.Model, req.Headers, req.OriginalRequest)
	body, _, err := rewriteRequestModel(req.OriginalRequest, upstreamModel)
	if err != nil {
		return fmt.Errorf("rewrite stream request: %w", err)
	}
	hostRaw, err := call(pluginabi.MethodHostModelExecuteStream, hostModelExecutePayload{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{
			EntryProtocol: req.SourceFormat,
			ExitProtocol:  req.Format,
			Model:         upstreamModel,
			Stream:        true,
			Body:          body,
			Headers:       req.Headers,
			Query:         req.Query,
			Alt:           req.Alt,
		},
		HostCallbackID: req.HostCallbackID,
	})
	if err != nil {
		return fmt.Errorf("execute stream: %w", err)
	}
	var hostResp struct {
		pluginapi.HostModelStreamResponse
		Body []byte `json:"body"`
	}
	if err := json.Unmarshal(hostRaw, &hostResp); err != nil {
		return fmt.Errorf("decode host stream response: %w", err)
	}
	if hostResp.StatusCode >= 400 {
		return fmt.Errorf("execute stream status %d: %s", hostResp.StatusCode, string(hostResp.Body))
	}
	if hostResp.StreamID == "" {
		return fmt.Errorf("missing host stream id")
	}
	hostStreamID := hostResp.StreamID
	closeHost := func() error {
		_, err := call(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: hostStreamID})
		return err
	}
	closePlugin := func(errText string) error {
		_, err := call(pluginabi.MethodHostStreamClose, struct {
			StreamID string `json:"stream_id"`
			Error    string `json:"error,omitempty"`
		}{StreamID: req.StreamID, Error: errText})
		return err
	}
	emit := func(payload []byte) error {
		_, err := call(pluginabi.MethodHostStreamEmit, struct {
			StreamID string `json:"stream_id"`
			Payload  []byte `json:"payload"`
		}{StreamID: req.StreamID, Payload: payload})
		return err
	}
	rewriter := newStreamChunkRewriter(originalModel)
	rewriter.frameRawJSONAsSSE = strings.Contains(strings.ToLower(hostResp.Headers.Get("Content-Type")), "text/event-stream")
	for {
		readRaw, err := call(pluginabi.MethodHostModelStreamRead, pluginapi.HostModelStreamReadRequest{StreamID: hostStreamID})
		if err != nil {
			_ = closeHost()
			return fmt.Errorf("read host stream: %w", err)
		}
		var chunk pluginapi.HostModelStreamReadResponse
		if err := json.Unmarshal(readRaw, &chunk); err != nil {
			_ = closeHost()
			return fmt.Errorf("decode host stream chunk: %w", err)
		}
		if chunk.Error != "" {
			flushed, flushErr := rewriter.Flush()
			if flushErr != nil {
				_ = closeHost()
				return fmt.Errorf("flush stream rewriter before error close: %w", flushErr)
			}
			for _, out := range flushed {
				if err := emit(out); err != nil {
					_ = closeHost()
					return fmt.Errorf("emit flushed stream chunk before error close: %w", err)
				}
			}
			if err := closeHost(); err != nil {
				return fmt.Errorf("close host stream: %w", err)
			}
			if err := closePlugin(chunk.Error); err != nil {
				return fmt.Errorf("close plugin stream: %w", err)
			}
			return nil
		}
		if chunk.Done {
			break
		}
		chunks, err := rewriter.Write(chunk.Payload)
		if err != nil {
			_ = closeHost()
			return fmt.Errorf("rewrite stream chunk: %w", err)
		}
		for _, out := range chunks {
			if err := emit(out); err != nil {
				_ = closeHost()
				return fmt.Errorf("emit stream chunk: %w", err)
			}
		}
	}
	flushed, err := rewriter.Flush()
	if err != nil {
		_ = closeHost()
		return fmt.Errorf("flush stream rewriter: %w", err)
	}
	for _, out := range flushed {
		if err := emit(out); err != nil {
			_ = closeHost()
			return fmt.Errorf("emit flushed stream chunk: %w", err)
		}
	}
	if err := closeHost(); err != nil {
		return fmt.Errorf("close host stream: %w", err)
	}
	if err := closePlugin(""); err != nil {
		return fmt.Errorf("close plugin stream: %w", err)
	}
	return nil
}

func handleExecutorExecute(raw []byte, call hostCaller) ([]byte, error) {
	var req executorRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	upstreamModel, originalModel := upstreamModelForRequest(req.SourceFormat, req.Model, req.Headers, req.OriginalRequest)
	body, _, err := rewriteRequestModel(req.OriginalRequest, upstreamModel)
	if err != nil {
		return nil, err
	}
	hostRaw, err := call(pluginabi.MethodHostModelExecute, hostModelExecutePayload{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{
			EntryProtocol: req.SourceFormat,
			ExitProtocol:  req.Format,
			Model:         upstreamModel,
			Stream:        false,
			Body:          body,
			Headers:       req.Headers,
			Query:         req.Query,
			Alt:           req.Alt,
		},
		HostCallbackID: req.HostCallbackID,
	})
	if err != nil {
		return nil, err
	}
	var hostResp pluginapi.HostModelExecutionResponse
	if err := json.Unmarshal(hostRaw, &hostResp); err != nil {
		return nil, err
	}
	if hostResp.StatusCode >= 400 {
		return nil, fmt.Errorf("host.model.execute status %d: %s", hostResp.StatusCode, string(hostResp.Body))
	}
	payload, _, err := restoreResponseModel(hostResp.Body, originalModel)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pluginapi.ExecutorResponse{Payload: payload, Headers: hostResp.Headers})
}

func okEnvelope(v any) ([]byte, error) {
	if v == nil {
		v = map[string]any{}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pluginabi.Envelope{OK: true, Result: raw})
}

func wrapEnvelope(payload []byte, err error) ([]byte, error) {
	if err != nil {
		var methodErr *pluginMethodError
		if errors.As(err, &methodErr) {
			return errorEnvelopeWithStatus(methodErr.Code, methodErr.Message, methodErr.HTTPStatus), nil
		}
		return errorEnvelope("plugin_error", err.Error()), nil
	}
	return okEnvelope(json.RawMessage(payload))
}

func errorEnvelope(code, message string) []byte {
	raw, err := json.Marshal(pluginabi.Envelope{
		OK:    false,
		Error: &pluginabi.Error{Code: code, Message: message},
	})
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"plugin_error","message":"failed to encode error envelope"}}`)
	}
	return raw
}

func errorEnvelopeWithStatus(code, message string, status int) []byte {
	raw, err := json.Marshal(pluginabi.Envelope{
		OK:    false,
		Error: &pluginabi.Error{Code: code, Message: message, HTTPStatus: status},
	})
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"plugin_error","message":"failed to encode error envelope"}}`)
	}
	return raw
}

// safeCall keeps a plugin-side panic from taking down the whole CPA process:
// this is a c-shared library loaded in-process, so an unrecovered panic is a
// host crash, not a failed request (mirrors key-policy's safePluginCall).
func safeCall(call func() ([]byte, error)) (response []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("plugin panic recovered", "panic", r)
			response = nil
			err = fmt.Errorf("plugin panic recovered: %v", r)
		}
	}()
	return call()
}

func handleMethod(method string, request []byte) ([]byte, error) {
	return safeCall(func() ([]byte, error) { return dispatchMethod(method, request) })
}

func dispatchMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister:
		return wrapEnvelope(handlePluginRegister(request))
	case pluginabi.MethodPluginReconfigure:
		return wrapEnvelope(handlePluginReconfigure(request))
	case pluginabi.MethodModelRoute:
		return wrapEnvelope(handleModelRoute(request))
	case pluginabi.MethodRequestInterceptBefore:
		return wrapEnvelope(handleRequestInterceptBefore(request))
	case pluginabi.MethodRequestInterceptAfter:
		return wrapEnvelope(handleRequestInterceptAfter(request))
	case pluginabi.MethodSchedulerPick:
		return wrapEnvelope(handleSchedulerPick(request))
	case pluginabi.MethodResponseInterceptAfter:
		return wrapEnvelope(handleResponseInterceptAfter(request))
	case pluginabi.MethodExecutorIdentifier:
		return wrapEnvelope(handleExecutorIdentifier())
	case pluginabi.MethodExecutorExecute:
		return wrapEnvelope(handleExecutorExecute(request, callHost))
	case pluginabi.MethodExecutorExecuteStream:
		return wrapEnvelope(handleExecutorExecuteStream(request, callHost))
	case pluginabi.MethodExecutorCountTokens:
		return errorEnvelope("unsupported", "executor.count_tokens is not supported by model-mapper-plus"), nil
	case pluginabi.MethodManagementRegister:
		return wrapEnvelope(handleManagementRegister())
	case pluginabi.MethodManagementHandle:
		return wrapEnvelope(handleManagement(request))
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

// lifecycleConfigYAML mirrors Config for YAML decoding. Pointer fields
// distinguish "key absent" from "key present with a zero value", so an omitted
// `enabled` keeps defaultConfig()'s true while `enabled: false` disables.
type lifecycleConfigYAML struct {
	UsageKeeperURL         *string `yaml:"usage_keeper_url"`
	UsageKeeperPasswordEnv *string `yaml:"usage_keeper_password_env"`
	Enabled                *bool   `yaml:"enabled"`
	GlobalRules            *string `yaml:"global_rules"`
	ClaudeMessagesRules    *string `yaml:"claude_messages_rules"`
	CodexResponsesRules    *string `yaml:"codex_responses_rules"`
	OpenAICompletionsRules *string `yaml:"openai_completions_rules"`
	StateFile              *string `yaml:"state_file"`
}

func decodeLifecycleConfig(raw []byte) (json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	var lifecycle struct {
		ConfigYAML string `json:"config_yaml"`
	}
	if err := json.Unmarshal(trimmed, &lifecycle); err != nil || lifecycle.ConfigYAML == "" {
		return append(json.RawMessage(nil), trimmed...), false, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(lifecycle.ConfigYAML)
	if err != nil {
		return nil, true, err
	}
	// Parse with a real YAML parser. The host re-marshals the user's config node
	// and preserves comments and nesting, so a hand-rolled line scanner
	// misreads "enabled: true # note" and nested keys like store.enabled.
	var doc lifecycleConfigYAML
	if err := yaml.Unmarshal(decoded, &doc); err != nil {
		return nil, true, fmt.Errorf("parse plugin config yaml: %w", err)
	}
	cfg := defaultConfig()
	if doc.UsageKeeperURL != nil {
		cfg.UsageKeeperURL = *doc.UsageKeeperURL
	}
	if doc.UsageKeeperPasswordEnv != nil {
		cfg.UsageKeeperPasswordEnv = *doc.UsageKeeperPasswordEnv
	}
	if doc.Enabled != nil {
		cfg.Enabled = *doc.Enabled
	}
	if doc.GlobalRules != nil {
		cfg.GlobalRules = *doc.GlobalRules
	}
	if doc.ClaudeMessagesRules != nil {
		cfg.ClaudeMessagesRules = *doc.ClaudeMessagesRules
	}
	if doc.CodexResponsesRules != nil {
		cfg.CodexResponsesRules = *doc.CodexResponsesRules
	}
	if doc.OpenAICompletionsRules != nil {
		cfg.OpenAICompletionsRules = *doc.OpenAICompletionsRules
	}
	if doc.StateFile != nil {
		cfg.StateFile = *doc.StateFile
	}
	cfgRaw, err := json.Marshal(cfg)
	if err != nil {
		return nil, true, err
	}
	return cfgRaw, true, nil
}

func callHost(method string, payload any) (json.RawMessage, error) {
	hostAPIMu.RLock()
	cb := hostCallbackFn
	hostAPIMu.RUnlock()
	if cb == nil {
		return nil, fmt.Errorf("host API not initialized")
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	responseBytes, err := cb(method, rawPayload)
	if err != nil {
		return nil, err
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(responseBytes, &env); err != nil {
		return nil, fmt.Errorf("decode host envelope: %w", err)
	}
	if !env.OK {
		if env.Error == nil {
			return nil, fmt.Errorf("host callback %s failed", method)
		}
		return nil, fmt.Errorf("host callback %s failed: %s", method, env.Error.Message)
	}
	return append(json.RawMessage(nil), env.Result...), nil
}

func rewriteTopLevelModel(body []byte, model string) ([]byte, bool, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return append([]byte(nil), body...), false, nil
	}
	changed := rewriteStringField(doc, "model", model)
	if !changed {
		return append([]byte(nil), body...), false, nil
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func rewriteResponseModelFields(body []byte, model string) ([]byte, bool, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return append([]byte(nil), body...), false, nil
	}
	changed := rewriteModelFields(doc, model)
	if !changed {
		return append([]byte(nil), body...), false, nil
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func rewriteModelFields(doc map[string]any, model string) bool {
	changed := rewriteStringField(doc, "model", model)
	changed = rewriteStringField(doc, "modelVersion", model) || changed
	changed = rewriteNestedModelFields(doc, "message", model) || changed
	changed = rewriteNestedModelFields(doc, "response", model) || changed
	return changed
}

func rewriteNestedModelFields(doc map[string]any, key string, model string) bool {
	nested, ok := doc[key].(map[string]any)
	if !ok {
		return false
	}
	changed := rewriteStringField(nested, "model", model)
	changed = rewriteStringField(nested, "modelVersion", model) || changed
	return changed
}

func rewriteStringField(doc map[string]any, key string, model string) bool {
	if _, ok := doc[key].(string); !ok {
		return false
	}
	doc[key] = model
	return true
}

type token struct {
	literal string
	capture int
}

type caseOperation uint8

const (
	caseOperationNone caseOperation = iota
	caseOperationLower
	caseOperationUpper
)

type rule struct {
	patternTokens     []token
	replacementTokens []token
	captureCount      int
	caseOperation     caseOperation
}

func defaultConfig() Config {
	return Config{Enabled: true, StateFile: defaultStateFile, UsageKeeperPasswordEnv: "CPA_KEEPER_LOGIN_PASSWORD"}
}

func parseRules(raw string) ([]rule, error) {
	if raw == "" {
		return nil, fmt.Errorf("empty rules")
	}
	for _, r := range raw {
		if unicode.IsSpace(r) || r == '"' || r == '\'' {
			return nil, fmt.Errorf("invalid character")
		}
		// Control characters usually mean a YAML double-quoted scalar ate the
		// escape (e.g. "\a" became BEL). Say so instead of "invalid rule".
		if r < 0x20 || r == 0x7f {
			return nil, fmt.Errorf("invalid control character %#U (use single quotes in YAML so \\a and \\A stay literal)", r)
		}
	}

	parts, err := splitEscaped(raw, ';')
	if err != nil || len(parts) == 0 {
		return nil, fmt.Errorf("invalid rules")
	}
	out := make([]rule, 0, len(parts))
	for _, part := range parts {
		switch part {
		case `\a`:
			out = append(out, rule{caseOperation: caseOperationLower})
			continue
		case `\A`:
			out = append(out, rule{caseOperation: caseOperationUpper})
			continue
		}
		sep, ok := findRuleSeparator(part)
		if !ok {
			return nil, fmt.Errorf("invalid rule")
		}
		find, replace := part[:sep], part[sep+2:]
		if find == "" || replace == "" {
			return nil, fmt.Errorf("invalid rule")
		}
		pt, captures, err := parseFind(find)
		if err != nil {
			return nil, err
		}
		rt, err := parseReplace(replace, captures)
		if err != nil {
			return nil, err
		}
		out = append(out, rule{patternTokens: pt, replacementTokens: rt, captureCount: captures})
	}
	return out, nil
}

func findRuleSeparator(s string) (int, bool) {
	escaped := false
	sep := -1
	for i := 0; i+1 < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '=' && s[i+1] == '>' {
			if sep >= 0 {
				return -1, false
			}
			sep = i
		}
	}
	return sep, sep >= 0
}

func splitEscaped(s string, sep byte) ([]string, error) {
	var parts []string
	start := 0
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == sep {
			if i == start {
				return nil, fmt.Errorf("empty segment")
			}
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	if escaped {
		return nil, fmt.Errorf("dangling escape")
	}
	if start >= len(s) {
		return nil, fmt.Errorf("empty segment")
	}
	parts = append(parts, s[start:])
	return parts, nil
}

func parseFind(s string) ([]token, int, error) {
	var tokens []token
	lit := strings.Builder{}
	captures := 0
	flush := func() {
		if lit.Len() > 0 {
			tokens = append(tokens, token{literal: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' {
			if i+1 >= len(s) {
				return nil, 0, fmt.Errorf("dangling escape")
			}
			n := s[i+1]
			switch n {
			case '*', ';', '$', '\\':
				lit.WriteByte(n)
				i++
			case '=':
				if i+2 < len(s) && s[i+2] == '>' {
					lit.WriteString("=>")
					i += 2
				} else {
					return nil, 0, fmt.Errorf("invalid escape")
				}
			default:
				return nil, 0, fmt.Errorf("invalid escape")
			}
			continue
		}
		if c == '*' {
			flush()
			captures++
			tokens = append(tokens, token{capture: captures})
			continue
		}
		lit.WriteByte(c)
	}
	flush()
	return tokens, captures, nil
}

func parseReplace(s string, captures int) ([]token, error) {
	var tokens []token
	lit := strings.Builder{}
	flush := func() {
		if lit.Len() > 0 {
			tokens = append(tokens, token{literal: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' {
			if i+1 >= len(s) {
				return nil, fmt.Errorf("dangling escape")
			}
			// Accept the same escapes parseFind does, so a literal ';', '\',
			// '$' or '*' can be produced on the replacement side too.
			switch n := s[i+1]; n {
			case ';', '\\', '$', '*':
				lit.WriteByte(n)
				i++
			case '=':
				if i+2 < len(s) && s[i+2] == '>' {
					lit.WriteString("=>")
					i += 2
				} else {
					return nil, fmt.Errorf("invalid escape")
				}
			default:
				return nil, fmt.Errorf("invalid escape")
			}
			continue
		}
		if c != '$' {
			lit.WriteByte(c)
			continue
		}
		if i+1 >= len(s) || s[i+1] < '1' || s[i+1] > '9' {
			return nil, fmt.Errorf("invalid reference")
		}
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		var n int
		for k := i + 1; k < j; k++ {
			n = n*10 + int(s[k]-'0')
		}
		if n == 0 || n > captures {
			return nil, fmt.Errorf("invalid reference")
		}
		flush()
		tokens = append(tokens, token{capture: n})
		i = j - 1
	}
	flush()
	return tokens, nil
}

func applyASCIIModelCase(model string, operation caseOperation) string {
	converted := []byte(model)
	for i, c := range converted {
		switch operation {
		case caseOperationLower:
			if c >= 'A' && c <= 'Z' {
				converted[i] = c + ('a' - 'A')
			}
		case caseOperationUpper:
			if c >= 'a' && c <= 'z' {
				converted[i] = c - ('a' - 'A')
			}
		}
	}
	return string(converted)
}

func applyRules(model string, rules []rule) (string, bool, error) {
	current := model
	matchedAny := false
	for _, r := range rules {
		if r.caseOperation != caseOperationNone {
			current = applyASCIIModelCase(current, r.caseOperation)
			matchedAny = true
			continue
		}
		captures, ok := matchTokens(current, r.patternTokens)
		if !ok {
			continue
		}
		next := buildReplacement(r.replacementTokens, captures)
		if next == "" {
			// An all-empty capture would blank the model name. Skip the entry
			// and keep going instead of failing the whole request.
			logger.Warn("rule produced an empty model name, entry skipped", "model", current)
			continue
		}
		current = next
		matchedAny = true
	}
	return current, matchedAny, nil
}

// matchTokens matches s against tokens, backtracking over capture boundaries.
//
// A capture is bounded by the next literal token; candidate end positions are
// the successive occurrences of that literal, tried nearest-first. Trying the
// nearest occurrence first preserves the result of every pattern that already
// matched, while the retry on later occurrences fixes patterns that used to
// fail outright when the literal repeats — `*-turbo` now matches
// `gpt-turbo-turbo` (capturing `gpt-turbo`) instead of not matching at all.
//
// Failed (tokenIndex, pos) pairs are memoized, which bounds the search to
// O(len(tokens)*len(s)) instead of the exponential blowup naive backtracking
// would allow on patterns with several captures.
//
// Adjacent captures (`a**b`) stay as before: the first one takes everything up
// to the literal and the second one captures the empty string.
func matchTokens(s string, tokens []token) ([]string, bool) {
	m := &tokenMatcher{s: s, tokens: tokens, failed: make(map[int]bool)}
	return m.match(0, 0, nil)
}

type tokenMatcher struct {
	s      string
	tokens []token
	failed map[int]bool
}

func (m *tokenMatcher) match(ti, pos int, captures []string) ([]string, bool) {
	if ti == len(m.tokens) {
		if pos == len(m.s) {
			return captures, true
		}
		return nil, false
	}
	memoKey := ti*(len(m.s)+1) + pos
	if m.failed[memoKey] {
		return nil, false
	}
	if out, ok := m.matchToken(ti, pos, captures); ok {
		return out, true
	}
	m.failed[memoKey] = true
	return nil, false
}

func (m *tokenMatcher) matchToken(ti, pos int, captures []string) ([]string, bool) {
	tok := m.tokens[ti]
	if tok.literal != "" {
		if !strings.HasPrefix(m.s[pos:], tok.literal) {
			return nil, false
		}
		return m.match(ti+1, pos+len(tok.literal), captures)
	}
	nextLit := ""
	for _, next := range m.tokens[ti+1:] {
		if next.literal != "" {
			nextLit = next.literal
			break
		}
	}
	if nextLit == "" {
		// Trailing capture: it must swallow the rest of the input.
		return m.match(ti+1, len(m.s), append(captures, m.s[pos:]))
	}
	for end := pos; end+len(nextLit) <= len(m.s); end++ {
		idx := strings.Index(m.s[end:], nextLit)
		if idx < 0 {
			break
		}
		end += idx
		next := append(append([]string(nil), captures...), m.s[pos:end])
		if out, ok := m.match(ti+1, end, next); ok {
			return out, true
		}
	}
	return nil, false
}

func buildReplacement(tokens []token, captures []string) string {
	var b strings.Builder
	for _, tok := range tokens {
		if tok.literal != "" {
			b.WriteString(tok.literal)
			continue
		}
		b.WriteString(captures[tok.capture-1])
	}
	return b.String()
}

func setHostCallbackForTest(cb hostCallback) {
	hostAPIMu.Lock()
	hostCallbackFn = cb
	hostAPIMu.Unlock()
}

func setHostCallback(cb hostCallback) {
	setHostCallbackForTest(cb)
}
