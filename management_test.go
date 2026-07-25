package main

import (
	"encoding/json"
	"net/http"
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
		"GET /plugins/model-mapper/state",
		"PUT /plugins/model-mapper/rules",
		"POST /plugins/model-mapper/keys",
		"PATCH /plugins/model-mapper/keys",
		"DELETE /plugins/model-mapper/keys",
		"POST /plugins/model-mapper/preview",
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
		Path:   "/v0/resource/plugins/model-mapper/index.html",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Headers.Get("Content-Type"), "text/html") {
		t.Fatalf("content-type = %q", resp.Headers.Get("Content-Type"))
	}
	lower := strings.ToLower(string(resp.Body))
	if !strings.Contains(lower, "<html") || !strings.Contains(lower, "model mapper") {
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
	resp := dispatchManagement(pluginapi.ManagementRequest{Method: http.MethodGet, Path: "/plugins/model-mapper/nope"})
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
