package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	// Register paths are relative under /v0/management/ (key-policy / SDK convention).
	managementRegisterBase = "/plugins/model-mapper-plus"
	// Handle paths are the full host-forwarded URL.Path.
	managementHandleBase = "/v0/management/plugins/model-mapper-plus"
	resourcePrefix       = "/v0/resource/plugins/model-mapper-plus"
)

func handleManagementRegister() ([]byte, error) {
	return json.Marshal(pluginapi.ManagementRegistrationResponse{
		Routes: []pluginapi.ManagementRoute{
			{Method: http.MethodPost, Path: managementRegisterBase + "/channel-credentials", Description: "Resolve AI Providers credential IDs without persisting configuration."},
			{Method: http.MethodGet, Path: managementRegisterBase + "/keeper/key-aliases", Description: "Read optional Keeper key aliases."},
			{Method: http.MethodPost, Path: managementRegisterBase + "/keeper/key-aliases/refresh", Description: "Refresh Keeper key aliases without saving bindings."},
			{Method: http.MethodGet, Path: managementRegisterBase + "/state", Description: "Read full model-mapper-plus state."},
			{Method: http.MethodPut, Path: managementRegisterBase + "/rules", Description: "Replace top-level rule sets."},
			{Method: http.MethodPost, Path: managementRegisterBase + "/keys", Description: "Create or replace a key binding."},
			{Method: http.MethodPatch, Path: managementRegisterBase + "/keys", Description: "Update a key binding by key."},
			{Method: http.MethodDelete, Path: managementRegisterBase + "/keys", Description: "Delete a key binding by key."},
			{Method: http.MethodPost, Path: managementRegisterBase + "/preview", Description: "Dry-run rule resolution."},
		},
		Resources: []pluginapi.ResourceRoute{
			{Path: "/index.html", Menu: "Model Mapper Plus", Description: "Model Mapper Plus admin UI."},
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
	// Browser resource GETs arrive through the same method without management
	// auth. Match the registered resource exactly rather than by prefix, so
	// unknown paths get a 404 instead of a 200 page.
	if req.Method == http.MethodGet && (path == resourcePrefix || path == resourcePrefix+"/index.html") {
		return serveIndexHTML()
	}
	switch {
	case req.Method == http.MethodPost && path == managementHandleBase+"/channel-credentials":
		return managementChannelCredentials(req)
	case req.Method == http.MethodGet && path == managementHandleBase+"/keeper/key-aliases":
		return managementKeeperAliases(false)
	case req.Method == http.MethodPost && path == managementHandleBase+"/keeper/key-aliases/refresh":
		return managementKeeperAliases(true)
	case req.Method == http.MethodGet && path == managementHandleBase+"/state":
		return managementGetState()
	case req.Method == http.MethodPut && path == managementHandleBase+"/rules":
		return managementPutRules(req)
	case req.Method == http.MethodPost && path == managementHandleBase+"/keys":
		return managementPostKey(req)
	case req.Method == http.MethodPatch && path == managementHandleBase+"/keys":
		return managementPatchKey(req)
	case req.Method == http.MethodDelete && path == managementHandleBase+"/keys":
		return managementDeleteKey(req)
	case req.Method == http.MethodPost && path == managementHandleBase+"/preview":
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
	Version       int          `json:"version"`
	Rules         RuleSet      `json:"rules"`
	KeyBindings   []KeyBinding `json:"key_bindings"`
	UpdatedAt     string       `json:"updated_at,omitempty"`
	Persisted     bool         `json:"persisted"`
	StateFile     string       `json:"state_file"`
	PluginVersion string       `json:"plugin_version"`
	// LoadError surfaces a rejected state_file so the fallback to the YAML
	// seed is visible in the UI instead of silently dropping key bindings.
	LoadError string `json:"load_error,omitempty"`
}

func managementGetState() pluginapi.ManagementResponse {
	st, persisted := loadedStateSnapshot()
	if st.KeyBindings == nil {
		st.KeyBindings = []KeyBinding{}
	}
	version := st.Version
	if version == 0 {
		version = stateVersion
	}
	return managementJSON(http.StatusOK, stateResponse{
		Version: version, Rules: st.Rules,
		KeyBindings: st.KeyBindings, UpdatedAt: st.UpdatedAt, Persisted: persisted,
		StateFile:     stateFilePath(),
		PluginVersion: pluginVersion,
		LoadError:     loadedStateLoadError(),
	})
}

func managementStateError(err error) pluginapi.ManagementResponse {
	if err == nil {
		return managementError(http.StatusInternalServerError, "unknown state error")
	}
	// Type check first: persistence errors ("rename …: invalid argument",
	// "invalid cross-device link") contain substrings the validation
	// heuristic below matches, and were being reported as 400 Bad Request.
	var persistErr *statePersistError
	if errors.As(err, &persistErr) {
		return managementError(http.StatusInternalServerError, err.Error())
	}
	// Validation / user-input failures are 400; persistence failures are 500 (spec).
	msg := err.Error()
	if strings.Contains(msg, "rules.") || strings.Contains(msg, "key_bindings") ||
		strings.Contains(msg, "key is required") || strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "invalid ") {
		return managementError(http.StatusBadRequest, msg)
	}
	// Default: treat as validation (applyStateUpdate mutate/validate path).
	return managementError(http.StatusBadRequest, msg)
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
		return managementStateError(err)
	}
	return managementGetState()
}

func managementPostKey(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	var binding KeyBinding
	if err := json.Unmarshal(req.Body, &binding); err != nil {
		return managementError(http.StatusBadRequest, "invalid key binding payload: "+err.Error())
	}
	// Store the trimmed key: validateState dedupes on the trimmed form and
	// keyFromRequest looks up by it, so an untrimmed key would be writable but
	// impossible to PATCH or DELETE afterwards.
	binding.Key = strings.TrimSpace(binding.Key)
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
		return managementStateError(err)
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
		Alias         *string        `json:"alias"`
		Enabled       *bool          `json:"enabled"`
		Blocked       *bool          `json:"blocked"`
		Rules         *RuleSet       `json:"rules"`
		ChannelTarget *ChannelTarget `json:"channel_target"`
		FastAllowed   *bool          `json:"fast_allowed"`
	}
	if err := json.Unmarshal(req.Body, &patch); err != nil {
		return managementError(http.StatusBadRequest, "invalid patch payload: "+err.Error())
	}
	// Fail before applyStateUpdate so missing keys never create/touch state_file (M2).
	if !keyBindingExists(target) {
		return managementError(http.StatusNotFound, "key binding not found")
	}
	err := applyStateUpdate(func(st *State) error {
		for i, b := range st.KeyBindings {
			if b.Key != target {
				continue
			}
			if patch.Alias != nil {
				st.KeyBindings[i].Alias = *patch.Alias
			}
			if patch.Enabled != nil {
				st.KeyBindings[i].Enabled = *patch.Enabled
			}
			if patch.Blocked != nil {
				st.KeyBindings[i].Blocked = *patch.Blocked
			}
			if patch.Rules != nil {
				st.KeyBindings[i].Rules = *patch.Rules
			}
			if patch.ChannelTarget != nil {
				st.KeyBindings[i].ChannelTarget = cloneChannelTarget(patch.ChannelTarget)
			}
			if patch.FastAllowed != nil {
				value := *patch.FastAllowed
				st.KeyBindings[i].FastAllowed = &value
			}
			return nil
		}
		return errKeyBindingNotFound
	})
	if err != nil {
		if err == errKeyBindingNotFound {
			return managementError(http.StatusNotFound, "key binding not found")
		}
		return managementStateError(err)
	}
	return managementGetState()
}

func managementDeleteKey(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	target := keyFromRequest(req)
	if target == "" {
		return managementError(http.StatusBadRequest, "key is required")
	}
	if !keyBindingExists(target) {
		return managementError(http.StatusNotFound, "key binding not found")
	}
	err := applyStateUpdate(func(st *State) error {
		for i, b := range st.KeyBindings {
			if b.Key == target {
				st.KeyBindings = append(st.KeyBindings[:i], st.KeyBindings[i+1:]...)
				return nil
			}
		}
		return errKeyBindingNotFound
	})
	if err != nil {
		if err == errKeyBindingNotFound {
			return managementError(http.StatusNotFound, "key binding not found")
		}
		return managementStateError(err)
	}
	return managementGetState()
}

type previewRequest struct {
	Key    string `json:"key"`
	Format string `json:"format"`
	Model  string `json:"model"`
}

type resolvedChannelTarget struct {
	Suppliers []string `json:"suppliers"`
	AuthIDs   []string `json:"auth_ids"`
}

type channelTargetPreview struct {
	Enabled  bool                  `json:"enabled"`
	Resolved resolvedChannelTarget `json:"resolved"`
}

type previewResponse struct {
	M1             string                `json:"m1"`
	M2             string                `json:"m2"`
	Routed         bool                  `json:"routed"`
	Final          string                `json:"final"`
	ChannelTarget  *channelTargetPreview `json:"channel_target,omitempty"`
	MappingSkipped bool                  `json:"mapping_skipped,omitempty"`
}

// previewRoute evaluates the same two-layer chain as routeModel (including the
// enabled gate) so dry-run matches production routing.
func previewRoute(cfg Config, src ruleSource, format, model, apiKey string) (previewResponse, error) {
	if !cfg.Enabled {
		return previewResponse{M1: model, M2: model, Routed: false, Final: model}, nil
	}
	if binding, targeted := findActiveChannelTarget(src.KeyBindings, apiKey); targeted {
		target := binding.ChannelTarget
		return previewResponse{
			M1: model, M2: model, Routed: false, Final: model, MappingSkipped: true,
			ChannelTarget: &channelTargetPreview{
				Enabled: true,
				Resolved: resolvedChannelTarget{
					Suppliers: append([]string(nil), target.Suppliers...),
					AuthIDs:   append([]string(nil), target.AuthIDs...),
				},
			},
		}, nil
	}
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
	resp, err := previewRoute(loadedConfig(), loadedRuleSource(), body.Format, body.Model, strings.TrimSpace(body.Key))
	if err != nil {
		return managementError(http.StatusBadRequest, err.Error())
	}
	return managementJSON(http.StatusOK, resp)
}
