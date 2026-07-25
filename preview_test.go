package main

import (
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Scenario: 分步结果可见
func TestPreviewRouteChained(t *testing.T) {
	src := ruleSource{
		Rules: RuleSet{Claude: `claude-opus-4-5(max)=>claude-opus-4-5(high)`},
		KeyBindings: []KeyBinding{{
			Key: "sk-k", Enabled: true,
			Rules: RuleSet{Claude: `claude-opus-4-5(high)=>claude-opus-4-5(medium)`},
		}},
	}
	got, err := previewRoute(Config{Enabled: true}, src, "claude", "claude-opus-4-5(max)", "sk-k")
	if err != nil {
		t.Fatal(err)
	}
	if got.M1 != "claude-opus-4-5(high)" || got.M2 != "claude-opus-4-5(medium)" {
		t.Fatalf("got %+v", got)
	}
	if !got.Routed || got.Final != "claude-opus-4-5(medium)" {
		t.Fatalf("routed/final wrong: %+v", got)
	}
}

func TestPreviewRouteNoMatch(t *testing.T) {
	got, err := previewRoute(Config{Enabled: true}, ruleSource{Rules: RuleSet{Global: "a=>b"}}, "openai", "gpt-4o", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Routed || got.M1 != "gpt-4o" || got.M2 != "gpt-4o" || got.Final != "gpt-4o" {
		t.Fatalf("got %+v", got)
	}
}

func TestPreviewRouteInvalidRules(t *testing.T) {
	src := ruleSource{Rules: RuleSet{Global: "a => b"}}
	if _, err := previewRoute(Config{Enabled: true}, src, "openai", "x", ""); err == nil {
		t.Fatal("want parse error surfaced")
	}
}

func TestManagementPreviewEndpoint(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true, ClaudeMessagesRules: `claude-opus-4-5(max)=>claude-opus-4-5(high)`})
	resp := managementPreview(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"format":"claude","model":"claude-opus-4-5(max)"}`),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, resp.Body)
	}
	var body previewResponse
	decodeBody(t, resp, &body)
	if body.M1 != "claude-opus-4-5(high)" || !body.Routed {
		t.Fatalf("got %+v", body)
	}
}

func TestPreviewRespectsDisabledPlugin(t *testing.T) {
	setupManagementTest(t, Config{Enabled: false, ClaudeMessagesRules: `a=>b`})
	resp := managementPreview(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"format":"claude","model":"a"}`),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	var body previewResponse
	decodeBody(t, resp, &body)
	if body.Routed || body.Final != "a" {
		t.Fatalf("disabled plugin must not map in preview: %+v", body)
	}
}
