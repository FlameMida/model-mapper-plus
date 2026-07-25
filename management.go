package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	managementBase = "/plugins/model-mapper"
	resourcePrefix = "/v0/resource/plugins/model-mapper"
)

func handleManagementRegister() ([]byte, error) {
	return json.Marshal(pluginapi.ManagementRegistrationResponse{
		Routes: []pluginapi.ManagementRoute{
			{Method: http.MethodGet, Path: managementBase + "/state", Description: "Read full model-mapper state."},
			{Method: http.MethodPut, Path: managementBase + "/rules", Description: "Replace top-level rule sets."},
			{Method: http.MethodPost, Path: managementBase + "/keys", Description: "Create or replace a key binding."},
			{Method: http.MethodPatch, Path: managementBase + "/keys", Description: "Update a key binding by key."},
			{Method: http.MethodDelete, Path: managementBase + "/keys", Description: "Delete a key binding by key."},
			{Method: http.MethodPost, Path: managementBase + "/preview", Description: "Dry-run rule resolution."},
		},
		Resources: []pluginapi.ResourceRoute{
			{Path: "/index.html", Menu: "Model Mapper", Description: "Model Mapper admin UI."},
		},
	})
}

func handleManagement(raw []byte) ([]byte, error) {
	var req pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	return json.Marshal(dispatchManagement(req))
}

func dispatchManagement(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	path := strings.TrimRight(req.Path, "/")
	// Browser resource GETs arrive through the same method without management auth.
	if req.Method == http.MethodGet && strings.HasPrefix(path, resourcePrefix) {
		return serveIndexHTML()
	}
	switch {
	case req.Method == http.MethodGet && path == managementBase+"/state":
		return managementGetState()
	case req.Method == http.MethodPut && path == managementBase+"/rules":
		return managementPutRules(req)
	case req.Method == http.MethodPost && path == managementBase+"/keys":
		return managementPostKey(req)
	case req.Method == http.MethodPatch && path == managementBase+"/keys":
		return managementPatchKey(req)
	case req.Method == http.MethodDelete && path == managementBase+"/keys":
		return managementDeleteKey(req)
	case req.Method == http.MethodPost && path == managementBase+"/preview":
		return managementPreview(req)
	}
	return managementError(http.StatusNotFound, "unknown management route")
}

func managementJSON(status int, v any) pluginapi.ManagementResponse {
	raw, err := json.Marshal(v)
	if err != nil {
		return managementError(http.StatusInternalServerError, err.Error())
	}
	return pluginapi.ManagementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       raw,
	}
}

func managementError(status int, msg string) pluginapi.ManagementResponse {
	return managementJSON(status, map[string]string{"error": msg})
}

type stateResponse struct {
	Version     int          `json:"version"`
	Rules       RuleSet      `json:"rules"`
	KeyBindings []KeyBinding `json:"key_bindings"`
	UpdatedAt   string       `json:"updated_at,omitempty"`
	Persisted   bool         `json:"persisted"`
}

func managementGetState() pluginapi.ManagementResponse {
	st, persisted := loadedStateSnapshot()
	if st.KeyBindings == nil {
		st.KeyBindings = []KeyBinding{}
	}
	return managementJSON(http.StatusOK, stateResponse{
		Version: stateVersion, Rules: st.Rules,
		KeyBindings: st.KeyBindings, UpdatedAt: st.UpdatedAt, Persisted: persisted,
	})
}

func managementPutRules(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	var body RuleSet
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return managementError(http.StatusBadRequest, "invalid rules payload: "+err.Error())
	}
	err := applyStateUpdate(func(st *State) error {
		st.Rules = body
		return nil
	})
	if err != nil {
		return managementError(http.StatusBadRequest, err.Error())
	}
	return managementGetState()
}

func managementPostKey(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	var binding KeyBinding
	if err := json.Unmarshal(req.Body, &binding); err != nil {
		return managementError(http.StatusBadRequest, "invalid key binding payload: "+err.Error())
	}
	err := applyStateUpdate(func(st *State) error {
		for i, b := range st.KeyBindings {
			if b.Key == binding.Key {
				st.KeyBindings[i] = binding // upsert
				return nil
			}
		}
		st.KeyBindings = append(st.KeyBindings, binding)
		return nil
	})
	if err != nil {
		return managementError(http.StatusBadRequest, err.Error())
	}
	return managementGetState()
}

// keyFromRequest locates the target binding key from query (?key=) or body {"key":...}.
func keyFromRequest(req pluginapi.ManagementRequest) string {
	if key := strings.TrimSpace(req.Query.Get("key")); key != "" {
		return key
	}
	var body struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(req.Body, &body)
	return strings.TrimSpace(body.Key)
}

func managementPatchKey(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	target := keyFromRequest(req)
	if target == "" {
		return managementError(http.StatusBadRequest, "key is required")
	}
	var patch struct {
		Alias   *string  `json:"alias"`
		Enabled *bool    `json:"enabled"`
		Rules   *RuleSet `json:"rules"`
	}
	if err := json.Unmarshal(req.Body, &patch); err != nil {
		return managementError(http.StatusBadRequest, "invalid patch payload: "+err.Error())
	}
	found := false
	err := applyStateUpdate(func(st *State) error {
		for i, b := range st.KeyBindings {
			if b.Key != target {
				continue
			}
			found = true
			if patch.Alias != nil {
				st.KeyBindings[i].Alias = *patch.Alias
			}
			if patch.Enabled != nil {
				st.KeyBindings[i].Enabled = *patch.Enabled
			}
			if patch.Rules != nil {
				st.KeyBindings[i].Rules = *patch.Rules
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return managementError(http.StatusBadRequest, err.Error())
	}
	if !found {
		return managementError(http.StatusNotFound, "key binding not found")
	}
	return managementGetState()
}

func managementDeleteKey(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	target := keyFromRequest(req)
	if target == "" {
		return managementError(http.StatusBadRequest, "key is required")
	}
	found := false
	err := applyStateUpdate(func(st *State) error {
		for i, b := range st.KeyBindings {
			if b.Key == target {
				st.KeyBindings = append(st.KeyBindings[:i], st.KeyBindings[i+1:]...)
				found = true
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return managementError(http.StatusBadRequest, err.Error())
	}
	if !found {
		return managementError(http.StatusNotFound, "key binding not found")
	}
	return managementGetState()
}

type previewRequest struct {
	Key    string `json:"key"`
	Format string `json:"format"`
	Model  string `json:"model"`
}

type previewResponse struct {
	M1     string `json:"m1"`
	M2     string `json:"m2"`
	Routed bool   `json:"routed"`
	Final  string `json:"final"`
}

// previewRoute evaluates the two-layer chain without the enabled gate:
// dry-run is a rule debugging tool (see plan task 5 note).
func previewRoute(src ruleSource, format, model, apiKey string) (previewResponse, error) {
	m1 := model
	mapped, matched, err := applyRuleSet(src.Rules, format, model)
	if err != nil {
		return previewResponse{}, err
	}
	if matched {
		m1 = mapped
	}
	m2 := m1
	if binding, ok := findKeyBinding(src.KeyBindings, apiKey); ok {
		mapped, matched, err = applyRuleSet(binding.Rules, format, m1)
		if err != nil {
			return previewResponse{}, err
		}
		if matched {
			m2 = mapped
		}
	}
	return previewResponse{M1: m1, M2: m2, Routed: m2 != model, Final: m2}, nil
}

func managementPreview(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	var body previewRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return managementError(http.StatusBadRequest, "invalid preview payload: "+err.Error())
	}
	if strings.TrimSpace(body.Model) == "" || strings.TrimSpace(body.Format) == "" {
		return managementError(http.StatusBadRequest, "format and model are required")
	}
	resp, err := previewRoute(loadedRuleSource(), body.Format, body.Model, strings.TrimSpace(body.Key))
	if err != nil {
		return managementError(http.StatusBadRequest, err.Error())
	}
	return managementJSON(http.StatusOK, resp)
}
