package main

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestChannelCredentialsFromHostResponses(t *testing.T) {
	raw, err := os.ReadFile("testdata/channel_credentials.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Config     json.RawMessage                    `json:"config"`
		Candidates []pluginapi.SchedulerAuthCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	setupManagementTest(t, Config{Enabled: true})
	resp := dispatchManagement(pluginapi.ManagementRequest{Method: http.MethodPost, Path: managementHandleBase + "/channel-credentials", Body: fixture.Config})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	var rows []struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
		Source   string `json:"source"`
		Status   string `json:"status"`
	}
	decodeBody(t, resp, &rows)
	if len(rows) != len(fixture.Candidates) {
		t.Fatalf("rows=%d candidates=%d", len(rows), len(fixture.Candidates))
	}
	for i, row := range rows {
		candidate := fixture.Candidates[i]
		if row.ID != candidate.ID || row.Provider != candidate.Provider || row.Source != "ai-provider" || row.Status != "configured" {
			t.Fatalf("row %d = %+v, host candidate = %+v", i, row, candidate)
		}
		t.Run(row.ID, func(t *testing.T) {
			seedSchedulerBinding(t, KeyBinding{Key: "proof-key", ChannelTarget: &ChannelTarget{Enabled: true, AuthIDs: []string{row.ID}}})
			selected := schedulerResponse(t, schedulerRequest("proof-key", fixture.Candidates...))
			if selected.AuthID != row.ID || !selected.Handled {
				t.Fatalf("selected=%+v", selected)
			}
		})
	}
	// Directory resolution is stateless and never retains upstream credentials.
	setupManagementTest(t, Config{Enabled: true})
	before := string(managementGetState().Body)
	dispatchManagement(pluginapi.ManagementRequest{Method: http.MethodPost, Path: managementHandleBase + "/channel-credentials", Body: fixture.Config})
	if string(managementGetState().Body) != before {
		t.Fatal("directory resolution mutated state")
	}
	for _, secret := range []string{"fake-key", "fake-other", "fake-one", "fake-two", "fake-disabled", "proxy.invalid", "A-Test"} {
		if strings.Contains(string(resp.Body), secret) {
			t.Fatalf("directory exposes %q", secret)
		}
	}
	var registration pluginapi.ManagementRegistrationResponse
	routes, err := handleManagementRegister()
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(routes, &registration); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, route := range registration.Routes {
		found = found || route.Method == http.MethodPost && route.Path == managementRegisterBase+"/channel-credentials"
	}
	if !found {
		t.Fatal("directory route not registered")
	}
}

func TestChannelCredentialsRejectInvalidInput(t *testing.T) {
	for _, body := range []string{`{`, `null`, `[]`, `{"codex-api-key":"secret"}`, `{"codex-api-key":[{"api-key":42}]}`} {
		resp := dispatchManagement(pluginapi.ManagementRequest{Method: http.MethodPost, Path: managementHandleBase + "/channel-credentials", Body: []byte(body)})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("input %s: status %d", body, resp.StatusCode)
		}
		if strings.Contains(string(resp.Body), "secret") {
			t.Fatal("error echoed input")
		}
	}
}

func TestChannelCredentialsMixedSelection(t *testing.T) {
	seedSchedulerBinding(t, KeyBinding{Key: "proof-key", ChannelTarget: &ChannelTarget{Enabled: true, AuthIDs: []string{"codex:apikey:chosen", "chosen.json"}}})
	candidates := []pluginapi.SchedulerAuthCandidate{
		{ID: "codex:apikey:chosen", Provider: "codex", Status: "active"},
		{ID: "codex:apikey:outside", Provider: "codex", Status: "active"},
		{ID: "chosen.json", Provider: "codex", Status: "active"},
		{ID: "outside.json", Provider: "codex", Status: "active"},
	}
	seen := map[string]int{}
	for range 6 {
		seen[schedulerResponse(t, schedulerRequest("proof-key", candidates...)).AuthID]++
	}
	if !reflect.DeepEqual(seen, map[string]int{"codex:apikey:chosen": 3, "chosen.json": 3}) {
		t.Fatalf("pool=%v", seen)
	}
	assertChannelTargetUnavailable(t, schedulerEnvelope(t, schedulerRequest("proof-key", candidates[1])), 1, 0, 2)
}
