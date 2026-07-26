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
// RUNNING CPA instance (typically the docker-compose one). It injects each
// case's rules / key bindings via the plugin's own management API, sends a
// chat request, and asserts the rewrite result.
//
// Required env:
//
//	CPA_SMOKE_MGMT_KEY   management key (remote-management.secret-key plaintext) for PUT/POST /rules,/keys
//	CPA_SMOKE_CLIENT_KEY a valid client api-key for /v1/chat/completions (also used as the bound key in key-chain cases)
//
// Optional env (defaults shown):
//
//	CPA_SMOKE_BASE_URL=http://127.0.0.1:8317
//	CPA_SMOKE_WRONG_KEY=wrong-local-smoke-key
//	CPA_SMOKE_MODEL_PASSTHROUGH=deepseek-v4-flash   # a model your upstream actually serves
//	CPA_SMOKE_MODEL_CHAIN_SRC=deepseek-v4-pro
//	CPA_SMOKE_MODEL_CHAIN_MID=deepseek-v4-flash
//	CPA_SMOKE_MODEL_CHAIN_DST=gpt-5.4-mini
//	CPA_SMOKE_MODEL_KEY_TEST=keytest-src            # fake name only resolvable via a key binding
//	CPA_SMOKE_MODEL_EFFORT_SRC=glm-5.2(max)         # model with thinking-effort suffix
//	CPA_SMOKE_MODEL_EFFORT_DST=glm-5.2(high)

const (
	defaultBaseURL  = "http://127.0.0.1:8317"
	defaultWrongKey = "wrong-local-smoke-key"
	rulesEndpoint   = "/v0/management/plugins/model-mapper-plus/rules"
	keysEndpoint    = "/v0/management/plugins/model-mapper-plus/keys"
)

type smokeEnv struct {
	baseURL   string
	mgmtKey   string
	clientKey string
	wrongKey  string
	models    map[string]string
}

type caseConfig struct {
	name              string
	pluginRules       string // injected into the openai ruleset segment
	keyBinding        string // if set, bind clientKey with this openai ruleset for the case
	requestModel      string
	useWrongKey       bool
	stream            bool
	wantSuccess       bool
	wantOriginalModel string
	forbidModel       string
	wantRulesReject   bool // expect PUT /rules to return 4xx (e.g. bad rules)
}

type openAIResponse struct {
	Model string          `json:"model"`
	Error json.RawMessage `json:"error"`
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
		baseURL:   env("CPA_SMOKE_BASE_URL", defaultBaseURL),
		mgmtKey:   mgmtKey,
		clientKey: clientKey,
		wrongKey:  env("CPA_SMOKE_WRONG_KEY", defaultWrongKey),
		models: map[string]string{
			"passthrough": env("CPA_SMOKE_MODEL_PASSTHROUGH", "deepseek-v4-flash"),
			"chainSrc":    env("CPA_SMOKE_MODEL_CHAIN_SRC", "deepseek-v4-pro"),
			"chainMid":    env("CPA_SMOKE_MODEL_CHAIN_MID", "deepseek-v4-flash"),
			"chainDst":    env("CPA_SMOKE_MODEL_CHAIN_DST", "gpt-5.4-mini"),
			"keyTest":     env("CPA_SMOKE_MODEL_KEY_TEST", "keytest-src"),
			"effortSrc":   env("CPA_SMOKE_MODEL_EFFORT_SRC", "glm-5.2(max)"),
			"effortDst":   env("CPA_SMOKE_MODEL_EFFORT_DST", "glm-5.2(high)"),
			"effortMid":   env("CPA_SMOKE_MODEL_EFFORT_MID", "glm-5.2(medium)"),
			"keyMid":      env("CPA_SMOKE_MODEL_KEY_MID", "keytest-mid"),
		},
	}

	if err := waitReady(envCfg); err != nil {
		return err
	}
	if err := clearRules(envCfg); err != nil {
		return fmt.Errorf("clear rules: %w", err)
	}
	defer func() { _ = clearRules(envCfg) }()
	defer func() { _ = deleteKey(envCfg, envCfg.clientKey) }()

	m := envCfg.models
	cases := []caseConfig{
		{name: "no-rules", requestModel: m["passthrough"], wantSuccess: true, wantOriginalModel: m["passthrough"]},
		{name: "openai-dedicated-chain", requestModel: m["chainSrc"], pluginRules: m["chainSrc"] + "=>" + m["chainMid"] + ";" + m["chainMid"] + "=>" + m["chainDst"], wantSuccess: true, wantOriginalModel: m["chainSrc"], forbidModel: m["chainDst"]},
		{name: "unmatched-model", requestModel: m["passthrough"], pluginRules: m["chainSrc"] + "=>" + m["chainDst"], wantSuccess: true, wantOriginalModel: m["passthrough"]},
		{name: "bad-rules", pluginRules: "bad rule", wantRulesReject: true},
		{name: "nonexistent-upstream-model", requestModel: m["chainSrc"], pluginRules: m["chainSrc"] + "=>definitely-not-a-real-upstream-model", wantSuccess: false},
		{name: "wrong-api-key", requestModel: m["chainMid"], useWrongKey: true, wantSuccess: false},
		{name: "streaming", requestModel: m["chainSrc"], pluginRules: m["chainSrc"] + "=>" + m["chainMid"] + ";" + m["chainMid"] + "=>" + m["chainDst"], stream: true, wantSuccess: true, wantOriginalModel: m["chainSrc"], forbidModel: m["chainDst"]},
		// key binding: keytest-src is not a real upstream model; only the bound key's
		// rule (keytest-src=>passthrough) makes the request succeed. Verifies the
		// two-layer chain (top-level empty -> key layer maps).
		{name: "key-binding-chain", requestModel: m["keyTest"], keyBinding: m["keyTest"] + "=>" + m["passthrough"], wantSuccess: true, wantOriginalModel: m["keyTest"]},
		// thinking-effort suffix: rule rewrites model(max)=>model(high); CPA strips
		// the (high) suffix when calling upstream. Verifies suffix participates in DSL.
		{name: "thinking-effort-suffix", requestModel: m["effortSrc"], pluginRules: m["effortSrc"] + "=>" + m["effortDst"], wantSuccess: true, wantOriginalModel: m["effortSrc"]},
		// top + key relay (both layers non-empty): top maps keyTest=>keyMid (both
		// fake), key maps keyMid=>passthrough. Only the two-layer chain reaches a
		// real upstream model; without the key layer the request dies at keyMid.
		{name: "top-key-relay", requestModel: m["keyTest"], pluginRules: m["keyTest"] + "=>" + m["keyMid"], keyBinding: m["keyMid"] + "=>" + m["passthrough"], wantSuccess: true, wantOriginalModel: m["keyTest"]},
		// key overrides top to net-zero: top passthrough=>keyTest (fake), key
		// keyTest=>passthrough (revert). Net == original -> plugin does not take
		// over -> CPA default serves passthrough. Without the key layer, the top
		// rule alone would route to the fake keyTest and fail upstream.
		{name: "key-overrides-top-netzero", requestModel: m["passthrough"], pluginRules: m["passthrough"] + "=>" + m["keyTest"], keyBinding: m["keyTest"] + "=>" + m["passthrough"], wantSuccess: true, wantOriginalModel: m["passthrough"]},
		// effort suffix + key relay: top (max)=>(high), key (high)=>(medium). Both
		// layers rewrite the suffix; CPA strips the final (medium) upstream.
		{name: "effort-suffix-key-relay", requestModel: m["effortSrc"], pluginRules: m["effortSrc"] + "=>" + m["effortDst"], keyBinding: m["effortDst"] + "=>" + m["effortMid"], wantSuccess: true, wantOriginalModel: m["effortSrc"]},
	}
	for _, tc := range cases {
		if err := runCase(envCfg, tc); err != nil {
			return fmt.Errorf("%s: %w", tc.name, err)
		}
		fmt.Printf("ok: %s\n", tc.name)
	}
	return nil
}

func runCase(env smokeEnv, tc caseConfig) error {
	if tc.wantRulesReject {
		status, _, err := putRules(env, tc.pluginRules)
		if err != nil {
			return err
		}
		if status/100 == 2 {
			return fmt.Errorf("want rules rejected (4xx), got status=%d", status)
		}
		return nil
	}

	if err := putRules2xx(env, tc.pluginRules); err != nil {
		return err
	}
	defer func() { _ = clearRules(env) }()

	if tc.keyBinding != "" {
		if err := putKey(env, env.clientKey, tc.keyBinding); err != nil {
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

func putRules(env smokeEnv, openaiRules string) (int, []byte, error) {
	body := map[string]string{"global": "", "claude": "", "codex": "", "openai": openaiRules}
	return doManage(env, http.MethodPut, rulesEndpoint, nil, body)
}

func putRules2xx(env smokeEnv, openaiRules string) error {
	status, respBody, err := putRules(env, openaiRules)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("put rules status=%d body=%s", status, respBody)
	}
	return nil
}

func clearRules(env smokeEnv) error { return putRules2xx(env, "") }

// --- key binding API ---

func putKey(env smokeEnv, apiKey, openaiRules string) error {
	body := map[string]any{
		"key":     apiKey,
		"alias":   "",
		"enabled": true,
		"rules":   map[string]string{"global": "", "claude": "", "codex": "", "openai": openaiRules},
	}
	status, respBody, err := doManage(env, http.MethodPost, keysEndpoint, nil, body)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("put key status=%d body=%s", status, respBody)
	}
	return nil
}

func deleteKey(env smokeEnv, apiKey string) error {
	status, _, err := doManage(env, http.MethodDelete, keysEndpoint, url.Values{"key": {apiKey}}, nil)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		// best-effort cleanup; ignore not-found
		if status != http.StatusNotFound {
			return fmt.Errorf("delete key status=%d", status)
		}
	}
	return nil
}

// --- shared ---

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

func runJSONCase(env smokeEnv, tc caseConfig, apiKey string) error {
	status, body, err := sendChatRequest(env, tc.requestModel, apiKey, false)
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
	status, body, err := sendChatRequest(env, tc.requestModel, apiKey, true)
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
		var parsed openAIResponse
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			continue
		}
		if parsed.Model == tc.wantOriginalModel {
			sawOriginal = true
		}
		if tc.forbidModel != "" && parsed.Model == tc.forbidModel {
			return fmt.Errorf("forbid streamed model %q in payload=%s", tc.forbidModel, payload)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan stream: %w", err)
	}
	if !sawOriginal {
		return fmt.Errorf("missing original streamed model %q in body=%s", tc.wantOriginalModel, body)
	}
	if !sawDone {
		return fmt.Errorf("missing data: [DONE] in body=%s", body)
	}
	return nil
}

func sendChatRequest(env smokeEnv, model, apiKey string, stream bool) (int, []byte, error) {
	payload := map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "say ok"}},
		"stream":   stream,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.baseURL+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}
