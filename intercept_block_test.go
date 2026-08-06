package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const wantBlockedResponseBody = `{"error":{"message":"Your quota has been exhausted.","type":"permission_error","code":"insufficient_quota"}}`

func setupBlockedInterceptTest(t *testing.T, cfg Config, bindings []KeyBinding) {
	t.Helper()
	setupManagementTest(t, cfg)
	for _, b := range bindings {
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		resp := managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: raw})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("seed binding status=%d body=%s", resp.StatusCode, resp.Body)
		}
	}
}

func interceptBeforeRaw(t *testing.T, headers http.Header) pluginapi.RequestInterceptResponse {
	t.Helper()
	req := pluginapi.RequestInterceptRequest{
		RequestID: "req-1",
		Headers:   headers,
		Model:     "gpt-test",
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	envRaw, err := handleMethod(pluginabi.MethodRequestInterceptBefore, raw)
	if err != nil {
		t.Fatalf("handleMethod: %v", err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(envRaw, &env); err != nil {
		t.Fatalf("envelope: %v raw=%s", err, envRaw)
	}
	if !env.OK {
		t.Fatalf("envelope not ok: %+v", env.Error)
	}
	var resp pluginapi.RequestInterceptResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("result: %v", err)
	}
	return resp
}

func TestInterceptBlocksBlockedKeyWithFixedBody(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-a", Enabled: true, Blocked: true},
	})
	resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-a"}})
	if !resp.Terminate {
		t.Fatal("want Terminate=true")
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", resp.StatusCode)
	}
	if !bytes.Equal(resp.ResponseBody, []byte(wantBlockedResponseBody)) {
		t.Fatalf("body=%s\nwant=%s", resp.ResponseBody, wantBlockedResponseBody)
	}
	if got := resp.ResponseHeaders.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q, want application/json", got)
	}
}

func TestInterceptBlocksWhenRulesDisabled(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-a", Enabled: false, Blocked: true},
	})
	resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-a"}})
	if !resp.Terminate || resp.StatusCode != 403 {
		t.Fatalf("want terminate 403, got terminate=%v status=%d", resp.Terminate, resp.StatusCode)
	}
}

func TestInterceptPassWhenNotBlocked(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-a", Enabled: true, Blocked: false, Rules: RuleSet{Global: "a=>b"}},
	})
	resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-a"}})
	if resp.Terminate {
		t.Fatal("must not terminate unblocked key")
	}
}

func TestInterceptPassWhenNoBinding(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, nil)
	resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-orphan"}})
	if resp.Terminate {
		t.Fatal("must not terminate unknown key")
	}
}

func TestInterceptPassWhenPluginDisabled(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: false}, []KeyBinding{
		{Key: "sk-a", Enabled: true, Blocked: true},
	})
	resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-a"}})
	if resp.Terminate {
		t.Fatal("plugin disabled must not terminate")
	}
}

func TestInterceptPassWhenNoClientKey(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-a", Enabled: true, Blocked: true},
	})
	resp := interceptBeforeRaw(t, http.Header{})
	if resp.Terminate {
		t.Fatal("missing client key must not terminate")
	}
}

func TestInterceptBlocksViaXAPIKeyFallback(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-x", Enabled: true, Blocked: true},
	})
	resp := interceptBeforeRaw(t, http.Header{"X-Api-Key": {"sk-x"}})
	if !resp.Terminate || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("x-api-key fallback did not block: %+v", resp)
	}
}

func TestInterceptBearerTakesPriorityOverXAPIKey(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-bearer", Enabled: true, Blocked: false},
		{Key: "sk-x", Enabled: true, Blocked: true},
	})
	resp := interceptBeforeRaw(t, http.Header{
		"Authorization": {"Bearer sk-bearer"},
		"X-Api-Key":     {"sk-x"},
	})
	if resp.Terminate {
		t.Fatal("unblocked Bearer must win over blocked x-api-key")
	}
}

func TestInterceptAfterIsPassThrough(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-a", Enabled: true, Blocked: true},
	})
	req := pluginapi.RequestInterceptRequest{
		RequestID: "req-2",
		Headers:   http.Header{"Authorization": {"Bearer sk-a"}},
		Body:      []byte(`{"model":"x"}`),
	}
	raw, _ := json.Marshal(req)
	envRaw, err := handleMethod(pluginabi.MethodRequestInterceptAfter, raw)
	if err != nil {
		t.Fatal(err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(envRaw, &env); err != nil || !env.OK {
		t.Fatalf("env=%+v err=%v raw=%s", env, err, envRaw)
	}
	var resp pluginapi.RequestInterceptResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resp, pluginapi.RequestInterceptResponse{}) {
		t.Fatalf("after response = %#v, want empty pass-through response", resp)
	}
}

func TestManagementUnblockAllowsInterceptPass(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{
		{Key: "sk-a", Enabled: true, Blocked: true},
	})
	if resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-a"}}); !resp.Terminate {
		t.Fatal("precondition: should block")
	}
	patch := managementPatchKey(pluginapi.ManagementRequest{
		Method: http.MethodPatch,
		Query:  url.Values{"key": {"sk-a"}},
		Body:   []byte(`{"blocked":false}`),
	})
	if patch.StatusCode != http.StatusOK {
		t.Fatalf("patch=%d", patch.StatusCode)
	}
	if resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-a"}}); resp.Terminate {
		t.Fatal("after unblock must pass")
	}
}

func TestManagementPostBlockedKeyImmediatelyRejects(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, nil)
	post := managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-new","enabled":true,"blocked":true,"rules":{}}`),
	})
	if post.StatusCode != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", post.StatusCode, post.Body)
	}
	resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-new"}})
	if !resp.Terminate || resp.StatusCode != http.StatusForbidden ||
		!bytes.Equal(resp.ResponseBody, []byte(wantBlockedResponseBody)) {
		t.Fatalf("new blocked binding did not reject immediately: %+v", resp)
	}
}

func TestManagementPostBlockedKeyStillRejectsAfterReconfigure(t *testing.T) {
	statePath := setupManagementTest(t, Config{Enabled: true})
	post := managementPostKey(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Body:   []byte(`{"key":"sk-reload","enabled":true,"blocked":true,"rules":{}}`),
	})
	if post.StatusCode != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", post.StatusCode, post.Body)
	}

	if _, err := handlePluginReconfigure(lifecycleRaw(t, "enabled: true\nstate_file: "+statePath+"\n")); err != nil {
		t.Fatalf("reconfigure: %v", err)
	}

	resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-reload"}})
	if !resp.Terminate || resp.StatusCode != http.StatusForbidden ||
		!bytes.Equal(resp.ResponseBody, []byte(wantBlockedResponseBody)) {
		t.Fatalf("reloaded blocked binding did not reject: %+v", resp)
	}
}

func TestManagementDeleteBlockedBindingAllowsInterceptPass(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true}, []KeyBinding{{
		Key:     "sk-delete",
		Enabled: true,
		Blocked: true,
	}})
	if resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-delete"}}); !resp.Terminate {
		t.Fatal("precondition: blocked binding should terminate")
	}

	deleted := managementDeleteKey(pluginapi.ManagementRequest{
		Method: http.MethodDelete,
		Query:  url.Values{"key": {"sk-delete"}},
	})
	if deleted.StatusCode != http.StatusOK {
		t.Fatalf("DELETE status=%d body=%s", deleted.StatusCode, deleted.Body)
	}
	if resp := interceptBeforeRaw(t, http.Header{"Authorization": {"Bearer sk-delete"}}); resp.Terminate {
		t.Fatalf("deleted binding must not terminate: %+v", resp)
	}
}
