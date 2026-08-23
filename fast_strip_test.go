package main

import (
	"encoding/json"
	"net/http"
	"testing"

	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func fastIntercept(t *testing.T, binding KeyBinding, req pluginapi.RequestInterceptRequest) pluginapi.RequestInterceptResponse {
	t.Helper()
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{binding})
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	result, err := handleRequestInterceptBefore(raw)
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.RequestInterceptResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func boolPointer(value bool) *bool { return &value }

func TestFastAllowedRequestRewrite(t *testing.T) {
	binding := KeyBinding{Key: "sk-k", FastAllowed: boolPointer(false)}

	t.Run("仅 body 带 speed", func(t *testing.T) {
		resp := fastIntercept(t, binding, pluginapi.RequestInterceptRequest{
			SourceFormat: "claude",
			Headers:      http.Header{"Authorization": {"Bearer sk-k"}},
			Body:         []byte(`{"model":"m","speed":"FaSt","messages":[]}`),
		})
		if resp.Terminate || string(resp.Body) != `{"model":"m","messages":[]}` {
			t.Fatalf("response = %+v body=%s", resp, resp.Body)
		}
	})

	t.Run("仅 beta 头标记", func(t *testing.T) {
		resp := fastIntercept(t, binding, pluginapi.RequestInterceptRequest{
			SourceFormat: "claude",
			Headers: http.Header{
				"Authorization":  {"Bearer sk-k"},
				"Anthropic-Beta": {"fast-mode-2026-02-01,prompt-caching-2024"},
			},
			Body: []byte(`{"model":"m"}`),
		})
		if got := resp.Headers.Values("Anthropic-Beta"); len(got) != 1 || got[0] != "prompt-caching-2024" {
			t.Fatalf("beta headers = %v clear=%v", got, resp.ClearHeaders)
		}
	})

	t.Run("默认放行", func(t *testing.T) {
		oldBinding := KeyBinding{Key: "sk-k"}
		resp := fastIntercept(t, oldBinding, pluginapi.RequestInterceptRequest{
			SourceFormat: "claude",
			Headers: http.Header{
				"Authorization":  {"Bearer sk-k"},
				"Anthropic-Beta": {"fast-mode-2026-02-01"},
			},
			Body: []byte(`{"model":"m","speed":"fast"}`),
		})
		if len(resp.Body) != 0 || len(resp.Headers) != 0 || len(resp.ClearHeaders) != 0 {
			t.Fatalf("old binding must pass through: %+v", resp)
		}
	})

	t.Run("非 Claude 与 blocked 优先级", func(t *testing.T) {
		resp := fastIntercept(t, KeyBinding{Key: "sk-k", Blocked: true, FastAllowed: boolPointer(false)}, pluginapi.RequestInterceptRequest{
			SourceFormat: "claude",
			Headers:      http.Header{"Authorization": {"Bearer sk-k"}},
			Body:         []byte(`{"speed":"fast"}`),
		})
		if !resp.Terminate || resp.StatusCode != http.StatusForbidden || len(resp.Body) != 0 {
			t.Fatalf("blocked must terminate before fast rewrite: %+v", resp)
		}

		pass := fastIntercept(t, binding, pluginapi.RequestInterceptRequest{
			SourceFormat: "openai",
			Headers:      http.Header{"Authorization": {"Bearer sk-k"}},
			Body:         []byte(`{"speed":"fast"}`),
		})
		if len(pass.Body) != 0 || len(pass.Headers) != 0 {
			t.Fatalf("non-Claude request changed: %+v", pass)
		}
	})
}

func TestFastBlockedUsesSingleRuleSourceSnapshot(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, nil)
	raw, err := json.Marshal(pluginapi.RequestInterceptRequest{
		SourceFormat: "claude",
		Headers:      http.Header{"Authorization": {"Bearer sk-k"}},
		Body:         []byte(`{"model":"m","speed":"fast"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	fastOff := false
	calls := 0
	load := func() ruleSource {
		calls++
		if calls == 1 {
			return ruleSource{KeyBindings: []KeyBinding{{Key: "sk-k", FastAllowed: &fastOff}}}
		}
		return ruleSource{KeyBindings: []KeyBinding{{Key: "sk-k", Blocked: true}}}
	}
	result, err := handleRequestInterceptBeforeWithRuleSource(raw, load)
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.RequestInterceptResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("rule source loads = %d, want 1", calls)
	}
	if resp.Terminate || string(resp.Body) != `{"model":"m"}` {
		t.Fatalf("response mixed snapshots: %+v body=%s", resp, resp.Body)
	}
}
