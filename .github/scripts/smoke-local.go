package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// smoke-local exercises the model-mapper-plus plugin against an ALREADY
// RUNNING CPA instance. It injects each case's rules / key bindings via the
// plugin's own management API (into a chosen ruleset segment), sends a
// request in a chosen format (openai/claude/codex), and asserts the rewrite.
//
// Required env:
//
//	CPA_SMOKE_MGMT_KEY   management key (plaintext)
//	CPA_SMOKE_CLIENT_KEY a valid client api-key (also the bound key)
//
// Optional env:
//
//	CPA_SMOKE_CLIENT_KEY_2    second client key (enables multi-key-isolation)
//	CPA_SMOKE_BASE_URL=http://127.0.0.1:8317
//	CPA_SMOKE_WRONG_KEY=wrong-local-smoke-key
//	CPA_SMOKE_MODEL_*         see defaults below

const (
	defaultBaseURL  = "http://127.0.0.1:8317"
	defaultWrongKey = "wrong-local-smoke-key"
	defaultSegment  = "openai"
	rulesEndpoint   = "/v0/management/plugins/model-mapper-plus/rules"
	keysEndpoint    = "/v0/management/plugins/model-mapper-plus/keys"
	previewEndpoint = "/v0/management/plugins/model-mapper-plus/preview"
)

type smokeEnv struct {
	baseURL   string
	mgmtKey   string
	clientKey string
	clientKey2 string
	wrongKey  string
	models    map[string]string
}

type caseConfig struct {
	name              string
	pluginRules       string
	topSegment        string
	keyBinding        string
	keySegment        string
	keyDisabled       bool // bind clientKey but with enabled=false
	requestModel      string
	format            string // "openai" (default), "claude", "codex"
	useWrongKey       bool
	stream            bool
	wantSuccess       bool
	wantOriginalModel string
	forbidModel       string
	wantRulesReject   bool
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func mustEnv(key string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%s is required", key)
}

func seg(s string) string {
	if s = strings.TrimSpace(s); s == "" {
		return defaultSegment
	}
	return s
}

func run() error {
	mgmtKey, err := mustEnv("CPA_SMOKE_MGMT_KEY")
	if err != nil {
		return err
	}
	clientKey, err := mustEnv("CPA_SMOKE_CLIENT_KEY")
	if err != nil {
		return err
	}
	envCfg := smokeEnv{
		baseURL:    env("CPA_SMOKE_BASE_URL", defaultBaseURL),
		mgmtKey:    mgmtKey,
		clientKey:  clientKey,
		clientKey2: strings.TrimSpace(os.Getenv("CPA_SMOKE_CLIENT_KEY_2")),
		wrongKey:   env("CPA_SMOKE_WRONG_KEY", defaultWrongKey),
		models: map[string]string{
			"passthrough": env("CPA_SMOKE_MODEL_PASSTHROUGH", "deepseek-v4-flash"),
			"chainSrc":    env("CPA_SMOKE_MODEL_CHAIN_SRC", "deepseek-v4-pro"),
			"chainMid":    env("CPA_SMOKE_MODEL_CHAIN_MID", "deepseek-v4-flash"),
			"chainDst":    env("CPA_SMOKE_MODEL_CHAIN_DST", "gpt-5.4-mini"),
			"keyTest":     env("CPA_SMOKE_MODEL_KEY_TEST", "keytest-src"),
			"keyMid":      env("CPA_SMOKE_MODEL_KEY_MID", "keytest-mid"),
			"keyWild":     env("CPA_SMOKE_MODEL_KEY_WILD", "keytest-wild"),
			"effortSrc":   env("CPA_SMOKE_MODEL_EFFORT_SRC", "glm-5.2(max)"),
			"effortDst":   env("CPA_SMOKE_MODEL_EFFORT_DST", "glm-5.2(high)"),
			"effortMid":   env("CPA_SMOKE_MODEL_EFFORT_MID", "glm-5.2(medium)"),
		},
	}

	if err := waitReady(envCfg); err != nil {
		return err
	}
	if err := clearAllRules(envCfg); err != nil {
		return fmt.Errorf("clear rules: %w", err)
	}
	// Clear any key bindings left by a previously interrupted run so they
	// cannot satisfy a wantSuccess case without the plugin actually running.
	_ = deleteKey(envCfg, envCfg.clientKey)
	_ = deleteKey(envCfg, envCfg.clientKey2)
	defer func() { _ = clearAllRules(envCfg) }()
	defer func() { _ = deleteKey(envCfg, envCfg.clientKey) }()
	defer func() { _ = deleteKey(envCfg, envCfg.clientKey2) }()

	m := envCfg.models
	cases := []caseConfig{
		{name: "no-rules", requestModel: m["passthrough"], wantSuccess: true, wantOriginalModel: m["passthrough"]},
		{name: "openai-dedicated-chain", requestModel: m["keyTest"], pluginRules: m["keyTest"] + "=>" + m["chainMid"] + ";" + m["chainMid"] + "=>" + m["chainDst"], wantSuccess: true, wantOriginalModel: m["keyTest"], forbidModel: m["chainDst"]},
		{name: "unmatched-model", requestModel: m["passthrough"], pluginRules: m["chainSrc"] + "=>" + m["chainDst"], wantSuccess: true, wantOriginalModel: m["passthrough"]},
		{name: "bad-rules", pluginRules: "bad rule", wantRulesReject: true},
		{name: "nonexistent-upstream-model", requestModel: m["chainSrc"], pluginRules: m["chainSrc"] + "=>definitely-not-a-real-upstream-model", wantSuccess: false},
		{name: "wrong-api-key", requestModel: m["chainMid"], useWrongKey: true, wantSuccess: false},
		{name: "streaming", requestModel: m["keyTest"], pluginRules: m["keyTest"] + "=>" + m["chainMid"] + ";" + m["chainMid"] + "=>" + m["chainDst"], stream: true, wantSuccess: true, wantOriginalModel: m["keyTest"], forbidModel: m["chainDst"]},

		{name: "key-binding-chain", requestModel: m["keyTest"], keyBinding: m["keyTest"] + "=>" + m["passthrough"], wantSuccess: true, wantOriginalModel: m["keyTest"]},
		{name: "thinking-effort-suffix", requestModel: m["keyTest"] + "(max)", pluginRules: m["keyTest"] + "(max)" + "=>" + m["effortDst"], wantSuccess: true, wantOriginalModel: m["keyTest"] + "(max)"},

		{name: "top-key-relay", requestModel: m["keyTest"], pluginRules: m["keyTest"] + "=>" + m["keyMid"], keyBinding: m["keyMid"] + "=>" + m["passthrough"], wantSuccess: true, wantOriginalModel: m["keyTest"]},
		{name: "key-overrides-top-netzero", requestModel: m["passthrough"], pluginRules: m["passthrough"] + "=>" + m["keyTest"], keyBinding: m["keyTest"] + "=>" + m["passthrough"], wantSuccess: true, wantOriginalModel: m["passthrough"]},
		{name: "effort-suffix-key-relay", requestModel: m["keyTest"] + "(max)", pluginRules: m["keyTest"] + "(max)" + "=>" + m["effortDst"], keyBinding: m["effortDst"] + "=>" + m["effortMid"], wantSuccess: true, wantOriginalModel: m["keyTest"] + "(max)"},

		{name: "top-global-segment-fallback", requestModel: m["keyTest"], pluginRules: m["keyTest"] + "=>" + m["passthrough"], topSegment: "global", wantSuccess: true, wantOriginalModel: m["keyTest"]},
		{name: "key-global-segment-fallback", requestModel: m["keyTest"], keyBinding: m["keyTest"] + "=>" + m["passthrough"], keySegment: "global", wantSuccess: true, wantOriginalModel: m["keyTest"]},

		{name: "wildcard-capture", requestModel: m["keyWild"], pluginRules: "keytest-*=>" + m["passthrough"], wantSuccess: true, wantOriginalModel: m["keyWild"]},
		{name: "capture-backref", requestModel: "cap-" + m["passthrough"], pluginRules: "cap-*=>$1", wantSuccess: true, wantOriginalModel: "cap-" + m["passthrough"]},

		{name: "claude-segment", requestModel: m["keyTest"], pluginRules: m["keyTest"] + "=>" + m["passthrough"], topSegment: "claude", format: "claude", wantSuccess: true, wantOriginalModel: m["keyTest"]},
		// codex-segment (/v1/responses) omitted: z.ai's anthropic endpoint rejects
		// CPA's responses->anthropic translation ("messages parameter is illegal"),
		// so the upstream cannot serve it even though the plugin's codex-segment
		// routing works. claude-segment already proves endpoint->segment routing.

		// disabled binding is skipped: top-level empty, bound rule would map
		// keyTest=>passthrough, but enabled=false so the key layer does not run
		// and keyTest reaches upstream as-is (fake name) -> failure.
		{name: "key-disabled-skips-key-layer", requestModel: m["keyTest"], keyBinding: m["keyTest"] + "=>" + m["passthrough"], keyDisabled: true, wantSuccess: false},

		{name: "claude-stream", requestModel: m["keyTest"], pluginRules: m["keyTest"] + "=>" + m["chainMid"] + ";" + m["chainMid"] + "=>" + m["chainDst"], topSegment: "claude", format: "claude", stream: true, wantSuccess: true, wantOriginalModel: m["keyTest"], forbidModel: m["chainDst"]},
	}
	for _, tc := range cases {
		if err := runCase(envCfg, tc); err != nil {
			return fmt.Errorf("%s: %w", tc.name, err)
		}
		fmt.Printf("ok: %s\n", tc.name)
	}

	if err := runPreviewConsistency(envCfg); err != nil {
		return fmt.Errorf("preview-api-consistency: %w", err)
	}
	fmt.Println("ok: preview-api-consistency")

	if envCfg.clientKey2 == "" {
		fmt.Println("skip: multi-key-isolation (set CPA_SMOKE_CLIENT_KEY_2 to enable)")
	} else if err := runMultiKeyCase(envCfg); err != nil {
		return fmt.Errorf("multi-key-isolation: %w", err)
	} else {
		fmt.Println("ok: multi-key-isolation")
	}
	return nil
}

// runMultiKeyCase: bind clientKey -> passthrough, clientKey2 -> fake keyMid.
// A request for keyTest with clientKey must succeed (its binding maps to a real
// model); with clientKey2 it must fail (its binding maps to a fake name).
// Proves two bindings coexist and are selected by the inbound key independently.
func runMultiKeyCase(env smokeEnv) error {
	m := env.models
	if err := putKeyAt(env, env.clientKey, "openai", m["keyTest"]+"=>"+m["passthrough"], true); err != nil {
		return err
	}
	defer func() { _ = deleteKey(env, env.clientKey) }()
	if err := putKeyAt(env, env.clientKey2, "openai", m["keyTest"]+"=>"+m["keyMid"], true); err != nil {
		return err
	}
	defer func() { _ = deleteKey(env, env.clientKey2) }()
	// key1 -> success
	st1, body1, err := sendChatRequest(env, m["keyTest"], env.clientKey, false)
	if err != nil {
		return err
	}
	if st1/100 != 2 {
		return fmt.Errorf("key1 want success, got status=%d body=%s", st1, body1)
	}
	var p1 openAIResponse
	if err := json.Unmarshal(body1, &p1); err != nil || p1.Model != m["keyTest"] {
		return fmt.Errorf("key1 want model %q, body=%s", m["keyTest"], body1)
	}
	// key2 -> failure (maps to fake keyMid)
	st2, _, err := sendChatRequest(env, m["keyTest"], env.clientKey2, false)
	if err != nil {
		return err
	}
	if st2/100 == 2 {
		return fmt.Errorf("key2 want failure (mapped to fake), got status=%d", st2)
	}
	return nil
}

type previewResult struct {
	M1     string `json:"m1"`
	M2     string `json:"m2"`
	Routed bool   `json:"routed"`
	Final  string `json:"final"`
}

func preview(env smokeEnv, apiKey, format, model string) (previewResult, error) {
	var pr previewResult
	body := map[string]string{"key": apiKey, "format": format, "model": model}
	status, respBody, err := doManage(env, http.MethodPost, previewEndpoint, nil, body)
	if err != nil {
		return pr, err
	}
	if status/100 != 2 {
		return pr, fmt.Errorf("preview status=%d body=%s", status, respBody)
	}
	if err := json.Unmarshal(respBody, &pr); err != nil {
		return pr, fmt.Errorf("decode preview: %w", err)
	}
	return pr, nil
}

// runPreviewConsistency: the dry-run /preview must agree with a real request.
// Top-level rule keyTest=>passthrough; preview must say routed+final=passthrough
// and a real request for the fake keyTest must actually succeed.
func runPreviewConsistency(env smokeEnv) error {
	m := env.models
	if err := putRulesAt2xx(env, "openai", m["keyTest"]+"=>"+m["passthrough"]); err != nil {
		return err
	}
	defer func() { _ = clearAllRules(env) }()
	pr, err := preview(env, "", "openai", m["keyTest"])
	if err != nil {
		return err
	}
	if !pr.Routed || pr.Final != m["passthrough"] {
		return fmt.Errorf("preview mismatch: want routed+final=%s, got %+v", m["passthrough"], pr)
	}
	st, _, err := sendChatRequest(env, m["keyTest"], env.clientKey, false)
	if err != nil {
		return err
	}
	if st/100 != 2 {
		return fmt.Errorf("preview said routed but real request failed: status=%d", st)
	}
	return nil
}

func runCase(env smokeEnv, tc caseConfig) error {
	if tc.wantRulesReject {
		status, _, err := putRulesAt(env, seg(tc.topSegment), tc.pluginRules)
		if err != nil {
			return err
		}
		if status/100 == 2 {
			return fmt.Errorf("want rules rejected (4xx), got status=%d", status)
		}
		return nil
	}

	if err := putRulesAt2xx(env, seg(tc.topSegment), tc.pluginRules); err != nil {
		return err
	}
	defer func() { _ = clearAllRules(env) }()

	if tc.keyBinding != "" {
		if err := putKeyAt(env, env.clientKey, seg(tc.keySegment), tc.keyBinding, !tc.keyDisabled); err != nil {
			return err
		}
		defer func() { _ = deleteKey(env, env.clientKey) }()
	}

	key := env.clientKey
	if tc.useWrongKey {
		key = env.wrongKey
	}
	if tc.stream {
		return runStreamCase(env, tc, key)
	}
	return runJSONCase(env, tc, key)
}

// --- rules API ---

func emptyRuleSet() map[string]string {
	return map[string]string{"global": "", "claude": "", "codex": "", "openai": ""}
}

func putRulesAt(env smokeEnv, segment, rules string) (int, []byte, error) {
	body := emptyRuleSet()
	body[segment] = rules
	return doManage(env, http.MethodPut, rulesEndpoint, nil, body)
}

func putRulesAt2xx(env smokeEnv, segment, rules string) error {
	status, respBody, err := putRulesAt(env, segment, rules)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("put rules (%s) status=%d body=%s", segment, status, respBody)
	}
	return nil
}

func clearAllRules(env smokeEnv) error {
	status, respBody, err := doManage(env, http.MethodPut, rulesEndpoint, nil, emptyRuleSet())
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("clear rules status=%d body=%s", status, respBody)
	}
	return nil
}

// --- key binding API ---

func putKeyAt(env smokeEnv, apiKey, segment, rules string, enabled bool) error {
	rs := emptyRuleSet()
	rs[segment] = rules
	body := map[string]any{"key": apiKey, "alias": "", "enabled": enabled, "rules": rs}
	status, respBody, err := doManage(env, http.MethodPost, keysEndpoint, nil, body)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("put key (%s) status=%d body=%s", segment, status, respBody)
	}
	return nil
}

func deleteKey(env smokeEnv, apiKey string) error {
	if apiKey == "" {
		return nil
	}
	status, _, err := doManage(env, http.MethodDelete, keysEndpoint, url.Values{"key": {apiKey}}, nil)
	if err != nil {
		return err
	}
	if status/100 != 2 && status != http.StatusNotFound {
		return fmt.Errorf("delete key status=%d", status)
	}
	return nil
}

// --- shared management ---

func doManage(env smokeEnv, method, path string, query url.Values, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	full := env.baseURL + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, full, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+env.mgmtKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody, nil
}

// --- readiness ---

func waitReady(env smokeEnv) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status, _, err := getModels(env)
		if err == nil && status/100 == 2 {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("CPA readiness timeout (is it running at " + env.baseURL + "?)")
}

func getModels(env smokeEnv) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.baseURL+"/v1/models", nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+env.clientKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// --- request senders ---

func sendRequest(env smokeEnv, tc caseConfig, apiKey string, stream bool) (int, []byte, error) {
	switch tc.format {
	case "claude":
		return sendClaudeRequest(env, tc.requestModel, apiKey, stream)
	case "codex":
		return sendCodexRequest(env, tc.requestModel, apiKey, stream)
	default:
		return sendChatRequest(env, tc.requestModel, apiKey, stream)
	}
}

func sendChatRequest(env smokeEnv, model, apiKey string, stream bool) (int, []byte, error) {
	payload := map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "say ok"}},
		"stream":   stream,
	}
	return sendJSON(env, http.MethodPost, "/v1/chat/completions", apiKey, payload, nil)
}

func sendClaudeRequest(env smokeEnv, model, apiKey string, stream bool) (int, []byte, error) {
	payload := map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "say ok"}},
		"max_tokens": 16,
		"stream":     stream,
	}
	extra := map[string]string{"anthropic-version": "2023-06-01"}
	return sendJSON(env, http.MethodPost, "/v1/messages", apiKey, payload, extra)
}

func sendCodexRequest(env smokeEnv, model, apiKey string, stream bool) (int, []byte, error) {
	payload := map[string]any{
		"model":  model,
		"input":  "say ok",
		"stream": stream,
	}
	return sendJSON(env, http.MethodPost, "/v1/responses", apiKey, payload, nil)
}

func sendJSON(env smokeEnv, method, path, apiKey string, payload any, extra map[string]string) (int, []byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, env.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("send %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// --- assertions ---

type openAIResponse struct {
	Model string          `json:"model"`
	Error json.RawMessage `json:"error"`
}

// streamModel extracts the model name from a SSE data payload across formats:
// openai exposes top-level .model; claude exposes .message.model.
func streamModel(payload string) string {
	var op struct {
		Model string `json:"model"`
	}
	if json.Unmarshal([]byte(payload), &op) == nil && op.Model != "" {
		return op.Model
	}
	var cl struct {
		Message struct {
			Model string `json:"model"`
		} `json:"message"`
	}
	json.Unmarshal([]byte(payload), &cl)
	return cl.Message.Model
}

func runJSONCase(env smokeEnv, tc caseConfig, apiKey string) error {
	status, body, err := sendRequest(env, tc, apiKey, false)
	if err != nil {
		return err
	}
	var parsed openAIResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		if !tc.wantSuccess && status/100 != 2 {
			return nil
		}
		return fmt.Errorf("decode response: %w (body=%s)", err, body)
	}
	if tc.wantSuccess {
		if status/100 != 2 || len(parsed.Error) != 0 {
			return fmt.Errorf("want success, got status=%d body=%s", status, body)
		}
		if tc.wantOriginalModel != "" && parsed.Model != tc.wantOriginalModel {
			return fmt.Errorf("want top-level model %q, got %q", tc.wantOriginalModel, parsed.Model)
		}
		if tc.forbidModel != "" && parsed.Model == tc.forbidModel {
			return fmt.Errorf("forbid top-level model %q, body=%s", tc.forbidModel, body)
		}
		return nil
	}
	if status/100 != 2 || len(parsed.Error) != 0 {
		if tc.forbidModel != "" && parsed.Model == tc.forbidModel {
			return fmt.Errorf("forbid top-level model %q in failure body=%s", tc.forbidModel, body)
		}
		return nil
	}
	if tc.forbidModel != "" && parsed.Model == tc.forbidModel {
		return fmt.Errorf("unexpected top-level model %q in success body=%s", tc.forbidModel, body)
	}
	return fmt.Errorf("expected failure, got status=%d body=%s", status, body)
}

func runStreamCase(env smokeEnv, tc caseConfig, apiKey string) error {
	status, body, err := sendRequest(env, tc, apiKey, true)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("want stream success, got status=%d body=%s", status, body)
	}
	sawDone := false
	sawOriginal := false
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			sawDone = true
			continue
		}
		// claude/anthropic streams end with a message_stop event, not [DONE].
		var typed struct{ Type string `json:"type"` }
		if json.Unmarshal([]byte(payload), &typed) == nil && typed.Type == "message_stop" {
			sawDone = true
			continue
		}
		model := streamModel(payload)
		if model == "" {
			continue
		}
		if tc.wantOriginalModel != "" && model == tc.wantOriginalModel {
			sawOriginal = true
		}
		if tc.forbidModel != "" && model == tc.forbidModel {
			return fmt.Errorf("forbid streamed model %q in payload=%s", tc.forbidModel, payload)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan stream: %w", err)
	}
	if tc.wantOriginalModel != "" && !sawOriginal {
		return fmt.Errorf("missing original streamed model %q in body=%s", tc.wantOriginalModel, body)
	}
	if !sawDone {
		return fmt.Errorf("missing data: [DONE] in body=%s", body)
	}
	return nil
}
