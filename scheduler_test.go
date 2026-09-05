package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	pluginapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func schedulerEnvelope(t *testing.T, req pluginapi.SchedulerPickRequest) pluginabi.Envelope {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	envRaw, err := handleMethod(pluginabi.MethodSchedulerPick, raw)
	if err != nil {
		t.Fatalf("handleMethod: %v", err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(envRaw, &env); err != nil {
		t.Fatalf("decode envelope: %v raw=%s", err, envRaw)
	}
	return env
}

func assertChannelTargetAuthNotFound(t *testing.T, env pluginabi.Envelope) {
	t.Helper()
	if env.OK || env.Error == nil || env.Error.Code != "auth_not_found" || env.Error.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("envelope = %+v", env)
	}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(env.Error.Message), &body); err != nil {
		t.Fatalf("error message is not JSON: %v message=%q", err, env.Error.Message)
	}
	if body.Error.Type != "auth_not_found" || body.Error.Code != "auth_not_found" || body.Error.Message == "" {
		t.Fatalf("error body = %+v", body)
	}
}

func schedulerResponse(t *testing.T, req pluginapi.SchedulerPickRequest) pluginapi.SchedulerPickResponse {
	t.Helper()
	env := schedulerEnvelope(t, req)
	if !env.OK {
		t.Fatalf("scheduler envelope error: %+v", env.Error)
	}
	var resp pluginapi.SchedulerPickResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func seedSchedulerBinding(t *testing.T, binding KeyBinding) {
	t.Helper()
	setupManagementTest(t, Config{Enabled: true})
	raw, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	resp := managementPostKey(pluginapi.ManagementRequest{Method: http.MethodPost, Body: raw})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed binding status=%d body=%s", resp.StatusCode, resp.Body)
	}
	channelTargetRoundRobin.reset()
	t.Cleanup(channelTargetRoundRobin.reset)
}

func schedulerRequest(apiKey string, candidates ...pluginapi.SchedulerAuthCandidate) pluginapi.SchedulerPickRequest {
	return pluginapi.SchedulerPickRequest{
		Provider:   "mixed",
		Providers:  []string{"gemini", "claude"},
		Model:      "model-m",
		Options:    pluginapi.SchedulerOptions{Headers: http.Header{"Authorization": {"Bearer " + apiKey}}},
		Candidates: candidates,
	}
}

func TestChannelTargetScheduler(t *testing.T) {
	t.Run("单选认证文件命中", func(t *testing.T) {
		seedSchedulerBinding(t, KeyBinding{Key: "sk-k", ChannelTarget: &ChannelTarget{Enabled: true, AuthIDs: []string{"f1"}}})
		resp := schedulerResponse(t, schedulerRequest("sk-k",
			pluginapi.SchedulerAuthCandidate{ID: "outside", Provider: "claude", Status: "active"},
			pluginapi.SchedulerAuthCandidate{ID: "f1", Provider: "claude", Status: "active"},
		))
		if !resp.Handled || resp.AuthID != "f1" || resp.DelegateBuiltin != "" {
			t.Fatalf("response = %+v", resp)
		}
	})

	t.Run("供应商整选动态入池", func(t *testing.T) {
		seedSchedulerBinding(t, KeyBinding{Key: "sk-k", ChannelTarget: &ChannelTarget{Enabled: true, Suppliers: []string{"Gemini"}}})
		first := schedulerResponse(t, schedulerRequest("sk-k",
			pluginapi.SchedulerAuthCandidate{ID: "g1", Provider: "gemini", Status: "active"},
		))
		if first.AuthID != "g1" {
			t.Fatalf("first = %+v", first)
		}
		seen := map[string]bool{}
		for i := 0; i < 2; i++ {
			resp := schedulerResponse(t, schedulerRequest("sk-k",
				pluginapi.SchedulerAuthCandidate{ID: "g1", Provider: "gemini", Status: "active"},
				pluginapi.SchedulerAuthCandidate{ID: "g2", Provider: "GEMINI", Status: "active"},
			))
			seen[resp.AuthID] = true
		}
		if !seen["g1"] || !seen["g2"] {
			t.Fatalf("dynamic provider candidates not both selected: %v", seen)
		}
	})

	t.Run("池内多凭据轮转分摊", func(t *testing.T) {
		seedSchedulerBinding(t, KeyBinding{Key: "sk-k", ChannelTarget: &ChannelTarget{Enabled: true, AuthIDs: []string{"c", "a", "b"}}})
		want := []string{"a", "b", "c", "a", "b", "c"}
		for i, id := range want {
			resp := schedulerResponse(t, schedulerRequest("sk-k",
				pluginapi.SchedulerAuthCandidate{ID: "outside", Provider: "gemini", Status: "active"},
				pluginapi.SchedulerAuthCandidate{ID: "c", Provider: "claude", Status: "active"},
				pluginapi.SchedulerAuthCandidate{ID: "a", Provider: "claude", Status: "active"},
				pluginapi.SchedulerAuthCandidate{ID: "b", Provider: "claude", Status: "active"},
			))
			if resp.AuthID != id {
				t.Fatalf("pick %d = %q, want %q", i, resp.AuthID, id)
			}
		}
	})

	t.Run("无法识别上下文时 fail-open", func(t *testing.T) {
		seedSchedulerBinding(t, KeyBinding{Key: "sk-k", ChannelTarget: &ChannelTarget{Enabled: true, AuthIDs: []string{"f1"}}})
		for _, req := range []pluginapi.SchedulerPickRequest{
			{Candidates: []pluginapi.SchedulerAuthCandidate{{ID: "f1", Status: "active"}}},
			schedulerRequest("sk-other", pluginapi.SchedulerAuthCandidate{ID: "f1", Status: "active"}),
		} {
			resp := schedulerResponse(t, req)
			if resp.Handled {
				t.Fatalf("fail-open response = %+v", resp)
			}
		}
	})

	t.Run("池内候选全部不可用时返回协议兼容 JSON 错误", func(t *testing.T) {
		seedSchedulerBinding(t, KeyBinding{Key: "sk-k", ChannelTarget: &ChannelTarget{Enabled: true, AuthIDs: []string{"f1"}}})
		env := schedulerEnvelope(t, schedulerRequest("sk-k",
			pluginapi.SchedulerAuthCandidate{ID: "outside", Provider: "claude", Status: "active"},
		))
		assertChannelTargetAuthNotFound(t, env)
	})

	t.Run("目标 cooldown、池外 active 时不越池", func(t *testing.T) {
		seedSchedulerBinding(t, KeyBinding{Key: "sk-k", ChannelTarget: &ChannelTarget{Enabled: true, AuthIDs: []string{"f1"}}})
		env := schedulerEnvelope(t, schedulerRequest("sk-k",
			pluginapi.SchedulerAuthCandidate{ID: "f1", Provider: "claude", Status: "cooldown"},
			pluginapi.SchedulerAuthCandidate{ID: "outside", Provider: "claude", Status: "active"},
		))
		assertChannelTargetAuthNotFound(t, env)
	})

	t.Run("目标低优先级、池外高优先级时不越池", func(t *testing.T) {
		seedSchedulerBinding(t, KeyBinding{Key: "sk-k", ChannelTarget: &ChannelTarget{Enabled: true, AuthIDs: []string{"f1"}}})
		// 模拟宿主只把全局最高优先级层 outside 交给 Scheduler；f1 已在回调前被排除。
		env := schedulerEnvelope(t, schedulerRequest("sk-k",
			pluginapi.SchedulerAuthCandidate{ID: "outside", Provider: "claude", Status: "active"},
		))
		assertChannelTargetAuthNotFound(t, env)
	})

	t.Run("宿主全局无候选 MAY 在 Scheduler 前返回 429", func(t *testing.T) {
		// 固定 SDK 的宿主边界回归；不是插件对定向请求的 429 保证。
		model := "model-m"
		next := time.Now().Add(time.Minute)
		auths := []*cliproxyauth.Auth{{
			ID: "f1", Provider: "claude",
			ModelStates: map[string]*cliproxyauth.ModelState{model: {
				Status: cliproxyauth.StatusActive, Unavailable: true, NextRetryAfter: next,
				Quota: cliproxyauth.QuotaState{Exceeded: true, NextRecoverAt: next},
			}},
		}}
		_, err := (&cliproxyauth.FillFirstSelector{}).Pick(context.Background(), "claude", model, cliproxyexecutor.Options{}, auths)
		if err == nil {
			t.Fatal("host selector must reject all-cooldown before scheduler.pick")
		}
		var statusErr interface{ StatusCode() int }
		var headerErr interface{ Headers() http.Header }
		if !errors.As(err, &statusErr) || statusErr.StatusCode() != http.StatusTooManyRequests {
			t.Fatalf("cooldown status error = %T %v", err, err)
		}
		if !errors.As(err, &headerErr) || headerErr.Headers().Get("Retry-After") == "" {
			t.Fatalf("cooldown headers missing: %T %v", err, err)
		}
	})
}

func TestChannelTargetSchedulerAcceptsHostReadyErrorStatus(t *testing.T) {
	const model = "gpt-5.6-sol"
	for _, scenario := range []string{"model_cooldown_expired", "auth_cooldown_expired", "other_model_failed"} {
		for _, targetKind := range []string{"auth_id", "supplier"} {
			t.Run(scenario+"/"+targetKind, func(t *testing.T) {
				target := &ChannelTarget{Enabled: true, AuthIDs: []string{"target"}}
				if targetKind == "supplier" {
					target = &ChannelTarget{Enabled: true, Suppliers: []string{"codex"}}
				}
				seedSchedulerBinding(t, KeyBinding{Key: "sk-k", ChannelTarget: target})

				auth := &cliproxyauth.Auth{
					ID: "target", Provider: "codex", Status: cliproxyauth.StatusError,
					Unavailable: true, NextRetryAfter: time.Unix(1, 0),
				}
				switch scenario {
				case "model_cooldown_expired":
					auth.ModelStates = map[string]*cliproxyauth.ModelState{model: {
						Status: cliproxyauth.StatusError, Unavailable: true, NextRetryAfter: time.Unix(1, 0),
					}}
				case "other_model_failed":
					auth.ModelStates = map[string]*cliproxyauth.ModelState{"other-model": {
						Status: cliproxyauth.StatusError, Unavailable: true, NextRetryAfter: time.Now().Add(time.Hour),
					}}
				}

				// The host decides readiness per model and recovery time, even when
				// the credential still carries the status of a previous failure.
				selected, err := (&cliproxyauth.FillFirstSelector{}).Pick(context.Background(), "codex", model, cliproxyexecutor.Options{}, []*cliproxyauth.Auth{auth})
				if err != nil || selected == nil {
					t.Fatalf("host must consider the target ready: selected=%v err=%v", selected, err)
				}
				req := schedulerRequest("sk-k",
					pluginapi.SchedulerAuthCandidate{ID: "outside", Provider: "claude", Status: "active"},
					pluginapi.SchedulerAuthCandidate{ID: selected.ID, Provider: selected.Provider, Status: string(selected.Status)},
				)
				req.Model = model
				req.Providers = []string{"codex", "claude"}
				resp := schedulerResponse(t, req)
				if !resp.Handled || resp.AuthID != "target" || resp.DelegateBuiltin != "" {
					t.Fatalf("must select the host-ready target without leaving the pool: %+v", resp)
				}
			})
		}
	}
}
