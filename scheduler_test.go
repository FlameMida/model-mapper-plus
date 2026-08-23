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

	t.Run("池内候选全部不可用", func(t *testing.T) {
		seedSchedulerBinding(t, KeyBinding{Key: "sk-k", ChannelTarget: &ChannelTarget{Enabled: true, AuthIDs: []string{"f1"}}})
		env := schedulerEnvelope(t, schedulerRequest("sk-k",
			pluginapi.SchedulerAuthCandidate{ID: "f1", Provider: "claude", Status: "cooldown"},
			pluginapi.SchedulerAuthCandidate{ID: "outside", Provider: "claude", Status: "active"},
		))
		if env.OK || env.Error == nil || env.Error.Code != "auth_not_found" || env.Error.HTTPStatus != http.StatusServiceUnavailable {
			t.Fatalf("envelope = %+v", env)
		}
	})

	t.Run("全部冷却走宿主原生应答", func(t *testing.T) {
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
