package main

import (
	"net/http"
	"testing"
)

// Scenario: Bearer 优先于 x-api-key
func TestAPIKeyFromHeadersBearerPreferred(t *testing.T) {
	h := http.Header{"Authorization": {"Bearer sk-a"}, "X-Api-Key": {"sk-b"}}
	if got := apiKeyFromHeaders(h); got != "sk-a" {
		t.Fatalf("got %q, want sk-a", got)
	}
}

func TestAPIKeyFromHeadersFallbacks(t *testing.T) {
	if got := apiKeyFromHeaders(http.Header{"X-Api-Key": {"sk-b"}}); got != "sk-b" {
		t.Fatalf("x-api-key got %q", got)
	}
	// 非 Bearer 的 Authorization 不当作 key，回退 x-api-key
	h := http.Header{"Authorization": {"Basic abc"}, "X-Api-Key": {"sk-c"}}
	if got := apiKeyFromHeaders(h); got != "sk-c" {
		t.Fatalf("non-bearer got %q", got)
	}
}

// Scenario: 无 key 头跳过 key 层
func TestAPIKeyFromHeadersEmpty(t *testing.T) {
	if got := apiKeyFromHeaders(nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func testConfig() Config {
	return Config{Enabled: true}
}

// Scenario: 接力降档
func TestRouteModelKeyChainDowngrade(t *testing.T) {
	src := ruleSource{
		Rules: RuleSet{Claude: `claude-opus-4-5(max)=>claude-opus-4-5(high)`},
		KeyBindings: []KeyBinding{{
			Key: "sk-k", Enabled: true,
			Rules: RuleSet{Claude: `claude-opus-4-5(high)=>claude-opus-4-5(medium)`},
		}},
	}
	d, err := routeModel(testConfig(), src, "claude", "claude-opus-4-5(max)", "sk-k")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Handled || d.UpstreamModel != "claude-opus-4-5(medium)" {
		t.Fatalf("got %+v", d)
	}
}

// Scenario: 未绑定 key 不受 key 层影响
func TestRouteModelUnboundKeyUntouched(t *testing.T) {
	src := ruleSource{
		Rules: RuleSet{Claude: `claude-opus-4-5(max)=>claude-opus-4-5(high)`},
		KeyBindings: []KeyBinding{{
			Key: "sk-k", Enabled: true,
			Rules: RuleSet{Claude: `claude-opus-4-5(high)=>claude-opus-4-5(medium)`},
		}},
	}
	d, err := routeModel(testConfig(), src, "claude", "claude-opus-4-5(max)", "sk-other")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Handled || d.UpstreamModel != "claude-opus-4-5(high)" {
		t.Fatalf("got %+v", d)
	}
}

// Scenario: key 端点段留空回退其全局段
func TestRouteModelKeyGlobalFallback(t *testing.T) {
	src := ruleSource{
		KeyBindings: []KeyBinding{{
			Key: "sk-k", Enabled: true,
			Rules: RuleSet{Global: `*(max)=>$1(high)`},
		}},
	}
	d, err := routeModel(testConfig(), src, "openai", "gpt-5(max)", "sk-k")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Handled || d.UpstreamModel != "gpt-5(high)" {
		t.Fatalf("got %+v", d)
	}
}

// Scenario: 绑定停用时透传顶层结果
func TestRouteModelBindingDisabled(t *testing.T) {
	src := ruleSource{
		Rules: RuleSet{Claude: `a=>b`},
		KeyBindings: []KeyBinding{{
			Key: "sk-k", Enabled: false, Blocked: false,
			Rules: RuleSet{Global: `b=>c`},
		}},
	}
	d, err := routeModel(testConfig(), src, "claude", "a", "sk-k")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Handled || d.UpstreamModel != "b" {
		t.Fatalf("got %+v", d)
	}
}

// Scenario: 净效果为零不路由（key 层改回原值）
func TestRouteModelNetZeroNotHandled(t *testing.T) {
	src := ruleSource{
		Rules: RuleSet{OpenAI: `gpt-4o=>deepseek-V3`},
		KeyBindings: []KeyBinding{{
			Key: "sk-k", Enabled: true,
			Rules: RuleSet{Global: `deepseek-V3=>gpt-4o`},
		}},
	}
	d, err := routeModel(testConfig(), src, "openai", "gpt-4o", "sk-k")
	if err != nil {
		t.Fatal(err)
	}
	if d.Handled {
		t.Fatalf("want unhandled, got %+v", d)
	}
}

// Scenario: 无 key 头跳过 key 层（路由维度）
func TestRouteModelNoKeySkipsKeyLayer(t *testing.T) {
	src := ruleSource{
		Rules: RuleSet{OpenAI: `gpt-4o=>deepseek-V3`},
		KeyBindings: []KeyBinding{{
			Key: "sk-k", Enabled: true,
			Rules: RuleSet{Global: `deepseek-V3=>qwen-max`},
		}},
	}
	d, err := routeModel(testConfig(), src, "openai", "gpt-4o", "")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Handled || d.UpstreamModel != "deepseek-V3" {
		t.Fatalf("got %+v", d)
	}
}
