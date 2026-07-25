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

// 临时桩：任务 4/5 替换为真实现
func managementGetState() pluginapi.ManagementResponse {
	return managementError(http.StatusNotImplemented, "state api pending")
}
func managementPutRules(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	return managementError(http.StatusNotImplemented, "rules api pending")
}
func managementPostKey(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	return managementError(http.StatusNotImplemented, "keys api pending")
}
func managementPatchKey(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	return managementError(http.StatusNotImplemented, "keys api pending")
}
func managementDeleteKey(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	return managementError(http.StatusNotImplemented, "keys api pending")
}
func managementPreview(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	return managementError(http.StatusNotImplemented, "preview api pending")
}
