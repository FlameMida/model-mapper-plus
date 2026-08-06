package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// blockedLifecycleHost links the SDK handler's model-router, request-interceptor,
// and executor hooks to the plugin's real RPC handlers. It records the host
// lifecycle without requiring a live CPA process.
type blockedLifecycleHost struct {
	t *testing.T

	callOrder      []string
	routeCalls     int
	interceptCalls int
	executorCalls  int
	routeTarget    pluginapi.ModelRouteTargetKind
}

func (h *blockedLifecycleHost) HasModelRouters() bool { return true }

func (h *blockedLifecycleHost) RouteModel(_ context.Context, req pluginapi.ModelRouteRequest) (pluginapi.ModelRouteResponse, bool) {
	h.t.Helper()
	h.callOrder = append(h.callOrder, "route")
	h.routeCalls++
	if got, want := req.Headers.Get("Authorization"), "Bearer sk-blocked"; got != want {
		h.t.Fatalf("model.route Authorization = %q, want %q", got, want)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		h.t.Fatalf("marshal route request: %v", err)
	}
	raw, err = handleModelRoute(raw)
	if err != nil {
		h.t.Fatalf("handle model route: %v", err)
	}
	var resp pluginapi.ModelRouteResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		h.t.Fatalf("decode route response: %v", err)
	}
	if !resp.Handled {
		h.t.Fatal("precondition: configured model route must be handled")
	}
	if h.routeTarget == pluginapi.ModelRouteTargetProvider {
		// A provider target exercises the ordinary AuthManager path. The real
		// plugin emits TargetSelf; this bridge isolates the host ordering after
		// the actual model-mapper route callback has reported a match.
		resp.TargetKind = pluginapi.ModelRouteTargetProvider
		resp.Target = "codex"
	} else {
		// pluginhost normalizes a self route to its executor plugin ID before the
		// SDK invokes PluginExecutorHost. The test host performs that same bridge.
		resp.TargetKind = pluginapi.ModelRouteTargetExecutor
		resp.Target = "model-mapper-plus"
	}
	return resp, true
}

func (h *blockedLifecycleHost) HasRequestInterceptors() bool { return true }

func (h *blockedLifecycleHost) InterceptRequestBeforeAuth(_ context.Context, req pluginapi.RequestInterceptRequest) pluginapi.RequestInterceptResponse {
	h.t.Helper()
	h.callOrder = append(h.callOrder, "intercept")
	h.interceptCalls++
	if got, want := req.Headers.Get("Authorization"), "Bearer sk-blocked"; got != want {
		h.t.Fatalf("request.intercept_before Authorization = %q, want %q", got, want)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		h.t.Fatalf("marshal intercept request: %v", err)
	}
	raw, err = handleRequestInterceptBefore(raw)
	if err != nil {
		h.t.Fatalf("handle request intercept: %v", err)
	}
	var resp pluginapi.RequestInterceptResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		h.t.Fatalf("decode intercept response: %v", err)
	}
	return resp
}

func (h *blockedLifecycleHost) InterceptRequestAfterAuth(_ context.Context, req pluginapi.RequestInterceptRequest) pluginapi.RequestInterceptResponse {
	return pluginapi.RequestInterceptResponse{Headers: req.Headers, Body: req.Body}
}

func (h *blockedLifecycleHost) InterceptResponse(_ context.Context, req pluginapi.ResponseInterceptRequest) pluginapi.ResponseInterceptResponse {
	return pluginapi.ResponseInterceptResponse{Headers: req.ResponseHeaders, Body: req.Body}
}

func (h *blockedLifecycleHost) InterceptStreamChunk(_ context.Context, req pluginapi.StreamChunkInterceptRequest) pluginapi.StreamChunkInterceptResponse {
	return pluginapi.StreamChunkInterceptResponse{Headers: req.ResponseHeaders, Body: req.Body}
}

func (h *blockedLifecycleHost) ExecutePluginExecutor(_ context.Context, _ string, _ coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	h.executorCalls++
	h.callOrder = append(h.callOrder, "executor")
	return coreexecutor.Response{Payload: []byte("unexpected executor call")}, nil
}

func (h *blockedLifecycleHost) ExecutePluginExecutorStream(_ context.Context, _ string, _ coreexecutor.Request, _ coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	h.executorCalls++
	h.callOrder = append(h.callOrder, "executor-stream")
	return nil, errors.New("unexpected executor stream call")
}

func (h *blockedLifecycleHost) CountPluginExecutor(_ context.Context, _ string, _ coreexecutor.Request, _ coreexecutor.Options) (coreexecutor.Response, error) {
	h.executorCalls++
	h.callOrder = append(h.callOrder, "executor-count")
	return coreexecutor.Response{}, errors.New("unexpected executor count call")
}

func lifecycleContext(t *testing.T) context.Context {
	t.Helper()
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer sk-blocked")
	ginCtx.Request = request
	return context.WithValue(context.Background(), "gin", ginCtx)
}

// CLIProxyAPI v7.2.119 calls the pure model.route callback before
// request.intercept_before. A blocked request may therefore be routed, but the
// interceptor must still stop it before the plugin executor or credential
// execution can reach an upstream.
func TestBlockedKeyHostLifecycleRoutesButStopsExecutor(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true, GlobalRules: "gpt-test=>mapped"}, []KeyBinding{{
		Key:     "sk-blocked",
		Enabled: false,
		Blocked: true,
	}})
	host := &blockedLifecycleHost{t: t}
	handler := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	handler.SetModelRouterHost(host)
	handler.SetPluginHost(host)

	body, responseHeaders, errMsg := handler.ExecuteWithAuthManager(lifecycleContext(t), "openai", "gpt-test", []byte(`{"model":"gpt-test"}`), "")
	if body != nil || responseHeaders != nil {
		t.Fatalf("terminated response = body %q headers %#v", body, responseHeaders)
	}
	if errMsg == nil || !errMsg.DirectResponse || errMsg.StatusCode != http.StatusForbidden || string(errMsg.Body) != wantBlockedResponseBody {
		t.Fatalf("termination error = %#v", errMsg)
	}
	if host.routeCalls != 1 {
		t.Fatalf("model.route calls = %d, want 1", host.routeCalls)
	}
	if host.interceptCalls != 1 {
		t.Fatalf("request.intercept_before calls = %d, want 1", host.interceptCalls)
	}
	if got, want := fmt.Sprint(host.callOrder), "[route intercept]"; got != want {
		t.Fatalf("host call order = %s, want %s", got, want)
	}
	if host.executorCalls != 0 {
		t.Fatalf("executor calls = %d, want 0", host.executorCalls)
	}
}

func TestBlockedKeyHostStreamLifecycleRoutesButStopsExecutor(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true, GlobalRules: "gpt-test=>mapped"}, []KeyBinding{{
		Key:     "sk-blocked",
		Enabled: false,
		Blocked: true,
	}})
	host := &blockedLifecycleHost{t: t}
	handler := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	handler.SetModelRouterHost(host)
	handler.SetPluginHost(host)

	dataChan, responseHeaders, errChan := handler.ExecuteStreamWithAuthManager(lifecycleContext(t), "openai", "gpt-test", []byte(`{"model":"gpt-test","stream":true}`), "")
	if dataChan != nil || responseHeaders != nil {
		t.Fatalf("terminated stream = data %v headers %#v", dataChan, responseHeaders)
	}
	errMsg, ok := <-errChan
	if !ok || errMsg == nil || !errMsg.DirectResponse || errMsg.StatusCode != http.StatusForbidden || string(errMsg.Body) != wantBlockedResponseBody {
		t.Fatalf("stream termination error = %#v ok=%v", errMsg, ok)
	}
	if host.routeCalls != 1 || host.interceptCalls != 1 {
		t.Fatalf("route/intercept calls = %d/%d, want 1/1", host.routeCalls, host.interceptCalls)
	}
	if got, want := fmt.Sprint(host.callOrder), "[route intercept]"; got != want {
		t.Fatalf("host call order = %s, want %s", got, want)
	}
	if host.executorCalls != 0 {
		t.Fatalf("executor calls = %d, want 0", host.executorCalls)
	}
}

func TestBlockedKeyHostProviderLifecycleStopsBeforeAuthExecution(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true, GlobalRules: "gpt-test=>mapped"}, []KeyBinding{{
		Key:     "sk-blocked",
		Enabled: false,
		Blocked: true,
	}})
	host := &blockedLifecycleHost{t: t, routeTarget: pluginapi.ModelRouteTargetProvider}
	handler := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	handler.SetModelRouterHost(host)
	handler.SetPluginHost(host)

	body, responseHeaders, errMsg := handler.ExecuteWithAuthManager(lifecycleContext(t), "openai", "gpt-test", []byte(`{"model":"gpt-test"}`), "")
	if body != nil || responseHeaders != nil {
		t.Fatalf("terminated provider response = body %q headers %#v", body, responseHeaders)
	}
	if errMsg == nil || !errMsg.DirectResponse || errMsg.StatusCode != http.StatusForbidden || string(errMsg.Body) != wantBlockedResponseBody {
		t.Fatalf("provider termination error = %#v", errMsg)
	}
	if got, want := fmt.Sprint(host.callOrder), "[route intercept]"; got != want {
		t.Fatalf("provider host call order = %s, want %s", got, want)
	}
	if host.executorCalls != 0 {
		t.Fatalf("provider executor calls = %d, want 0", host.executorCalls)
	}
}

func TestBlockedKeyHostProviderStreamLifecycleStopsBeforeAuthExecution(t *testing.T) {
	setupBlockedInterceptTest(t, Config{Enabled: true, GlobalRules: "gpt-test=>mapped"}, []KeyBinding{{
		Key:     "sk-blocked",
		Enabled: false,
		Blocked: true,
	}})
	host := &blockedLifecycleHost{t: t, routeTarget: pluginapi.ModelRouteTargetProvider}
	handler := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))
	handler.SetModelRouterHost(host)
	handler.SetPluginHost(host)

	dataChan, responseHeaders, errChan := handler.ExecuteStreamWithAuthManager(lifecycleContext(t), "openai", "gpt-test", []byte(`{"model":"gpt-test","stream":true}`), "")
	if dataChan != nil || responseHeaders != nil {
		t.Fatalf("terminated provider stream = data %v headers %#v", dataChan, responseHeaders)
	}
	errMsg, ok := <-errChan
	if !ok || errMsg == nil || !errMsg.DirectResponse || errMsg.StatusCode != http.StatusForbidden || string(errMsg.Body) != wantBlockedResponseBody {
		t.Fatalf("provider stream termination error = %#v ok=%v", errMsg, ok)
	}
	if got, want := fmt.Sprint(host.callOrder), "[route intercept]"; got != want {
		t.Fatalf("provider stream host call order = %s, want %s", got, want)
	}
	if host.executorCalls != 0 {
		t.Fatalf("provider stream executor calls = %d, want 0", host.executorCalls)
	}
}
