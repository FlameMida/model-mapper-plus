package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Scenario: 注册包含 management 声明
func TestManagementRegisterRoutes(t *testing.T) {
	raw, err := handleManagementRegister()
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.ManagementRegistrationResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	routes := map[string]bool{}
	for _, r := range resp.Routes {
		routes[r.Method+" "+r.Path] = true
	}
	for _, want := range []string{
		"GET /plugins/model-mapper-plus/audit",
		"GET /plugins/model-mapper-plus/state",
		"PUT /plugins/model-mapper-plus/rules",
		"POST /plugins/model-mapper-plus/keys",
		"PATCH /plugins/model-mapper-plus/keys",
		"DELETE /plugins/model-mapper-plus/keys",
		"POST /plugins/model-mapper-plus/preview",
	} {
		if !routes[want] {
			t.Fatalf("missing route %s in %v", want, routes)
		}
	}
	if len(resp.Resources) != 1 || resp.Resources[0].Path != "/index.html" {
		t.Fatalf("unexpected resources: %+v", resp.Resources)
	}
}

// Scenario: 管理页可访问
func TestDispatchManagementServesIndex(t *testing.T) {
	resp := dispatchManagement(pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/resource/plugins/model-mapper-plus/index.html",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Headers.Get("Content-Type"), "text/html") {
		t.Fatalf("content-type = %q", resp.Headers.Get("Content-Type"))
	}
	lower := strings.ToLower(string(resp.Body))
	if !strings.Contains(lower, "<html") || !strings.Contains(lower, "model mapper plus") {
		t.Fatal("body missing html or plugin marker")
	}
}

// Scenario: 注册能力含 management_api 且 ConfigFields 含 state_file
func TestPluginRegistrationDeclaresManagement(t *testing.T) {
	reg := pluginRegistration()
	if !reg.Capabilities.ManagementAPI {
		t.Fatal("management_api capability missing")
	}
	found := false
	for _, f := range reg.Metadata.ConfigFields {
		if f.Name == "state_file" {
			found = true
		}
	}
	if !found {
		t.Fatal("state_file config field missing")
	}
}

func TestDispatchManagementUnknown(t *testing.T) {
	resp := dispatchManagement(pluginapi.ManagementRequest{Method: http.MethodGet, Path: "/plugins/model-mapper-plus/nope"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// Scenario: 注册能力含 management_api（经 handleMethod 全流程）
func TestHandleMethodManagementRegister(t *testing.T) {
	raw, err := handleMethod("management.register", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"ok":true`) {
		t.Fatalf("unexpected envelope: %s", raw)
	}
}

func TestManagementChannelTargetAndFast(t *testing.T) {
	t.Run("校验拒绝重复 ID", func(t *testing.T) {
		statePath := setupManagementTest(t, Config{Enabled: true})
		resp := managementPostKey(pluginapi.ManagementRequest{
			Method: http.MethodPost,
			Body:   []byte(`{"key":"sk-k","channel_target":{"enabled":true,"auth_ids":["a","a"]}}`),
		})
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(resp.Body), "duplicate") {
			t.Fatalf("response = %d %s", resp.StatusCode, resp.Body)
		}
		if _, err := os.Stat(statePath); !os.IsNotExist(err) {
			t.Fatalf("rejected save changed state file: %v", err)
		}
	})

	t.Run("PATCH 局部更新 fast_allowed", func(t *testing.T) {
		setupManagementTest(t, Config{Enabled: true})
		managementPostKey(pluginapi.ManagementRequest{
			Method: http.MethodPost,
			Body:   []byte(`{"key":"sk-k","alias":"A","enabled":true,"rules":{"global":"a=>b"}}`),
		})
		resp := managementPatchKey(pluginapi.ManagementRequest{
			Method: http.MethodPatch,
			Query:  url.Values{"key": {"sk-k"}},
			Body:   []byte(`{"fast_allowed":false}`),
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH = %d %s", resp.StatusCode, resp.Body)
		}
		st, _ := loadedStateSnapshot()
		got := st.KeyBindings[0]
		if got.FastAllowed == nil || *got.FastAllowed || got.Alias != "A" || got.Rules.Global != "a=>b" || !got.Enabled {
			t.Fatalf("PATCH clobbered binding: %+v", got)
		}
	})

	t.Run("PATCH 更新渠道定向", func(t *testing.T) {
		setupManagementTest(t, Config{Enabled: true})
		managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: []byte(`{"key":"sk-k","enabled":true}`)})
		patch := func(body string) KeyBinding {
			resp := managementPatchKey(pluginapi.ManagementRequest{
				Method: http.MethodPatch,
				Query:  url.Values{"key": {"sk-k"}},
				Body:   []byte(body),
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("PATCH %s = %d %s", body, resp.StatusCode, resp.Body)
			}
			st, _ := loadedStateSnapshot()
			return st.KeyBindings[0]
		}
		got := patch(`{"channel_target":{"enabled":true,"suppliers":["gemini"],"auth_ids":["f1"]}}`)
		if got.ChannelTarget == nil || !got.ChannelTarget.Enabled {
			t.Fatalf("enabled target = %+v", got.ChannelTarget)
		}
		got = patch(`{"channel_target":null}`)
		if got.ChannelTarget == nil || !got.ChannelTarget.Enabled {
			t.Fatalf("null must preserve target: %+v", got.ChannelTarget)
		}
		got = patch(`{"channel_target":{"enabled":false,"suppliers":["gemini"],"auth_ids":["f1"]}}`)
		if got.ChannelTarget == nil || got.ChannelTarget.Enabled ||
			len(got.ChannelTarget.Suppliers) != 1 || got.ChannelTarget.Suppliers[0] != "gemini" ||
			len(got.ChannelTarget.AuthIDs) != 1 || got.ChannelTarget.AuthIDs[0] != "f1" {
			t.Fatalf("disabled target lost configuration: %+v", got.ChannelTarget)
		}
	})

	t.Run("preview 显示定向", func(t *testing.T) {
		setupManagementTest(t, Config{Enabled: true, GlobalRules: "model-m=>model-n"})
		managementPostKey(pluginapi.ManagementRequest{
			Method: http.MethodPost,
			Body:   []byte(`{"key":"sk-k","enabled":true,"rules":{"global":"model-n=>model-p"},"channel_target":{"enabled":true,"suppliers":["gemini"],"auth_ids":["f1"]}}`),
		})
		resp := managementPreview(pluginapi.ManagementRequest{
			Method: http.MethodPost,
			Body:   []byte(`{"key":"sk-k","format":"openai","model":"model-m"}`),
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("preview = %d %s", resp.StatusCode, resp.Body)
		}
		var got previewResponse
		decodeBody(t, resp, &got)
		if !got.MappingSkipped || got.M1 != "model-m" || got.M2 != "model-m" || got.Final != "model-m" || got.Routed {
			t.Fatalf("preview routing = %+v", got)
		}
		if got.ChannelTarget == nil || !got.ChannelTarget.Enabled ||
			len(got.ChannelTarget.Resolved.Suppliers) != 1 || got.ChannelTarget.Resolved.Suppliers[0] != "gemini" ||
			len(got.ChannelTarget.Resolved.AuthIDs) != 1 || got.ChannelTarget.Resolved.AuthIDs[0] != "f1" {
			t.Fatalf("preview target = %+v", got.ChannelTarget)
		}
	})
}
