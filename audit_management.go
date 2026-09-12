package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// This gate coordinates management side effects and reconfiguration only;
// inference continues to use the existing independent snapshot locks.
var managementMutationMu sync.Mutex

type auditResult struct {
	Outcome   string
	Changed   *bool
	Changes   map[string]auditChange
	ErrorCode string
}

func withAuditedManagement(req pluginapi.ManagementRequest, run func() (pluginapi.ManagementResponse, auditResult)) pluginapi.ManagementResponse {
	managementMutationMu.Lock()
	defer managementMutationMu.Unlock()
	before, _ := loadedStateSnapshot()
	cfg := loadedConfig()
	event := auditEventForRequest(req, before)
	if event.ObjectType == "keeper_auth_name" {
		event.ObjectRef = auditRedactText(event.ObjectRef, auditSecrets(req, cfg, before))
	}
	ticket, err := beginAudit(stateFilePath(), event)
	if err != nil {
		return managementError(http.StatusServiceUnavailable, "audit_unavailable")
	}
	response, result := run()
	after, _ := loadedStateSnapshot()
	secrets := auditSecrets(req, cfg, before, after)
	for field, change := range result.Changes {
		change.Before = auditRedactJSON(change.Before, secrets)
		change.After = auditRedactJSON(change.After, secrets)
		result.Changes[field] = change
	}
	event.Outcome, event.Changed, event.Changes, event.ErrorCode = result.Outcome, result.Changed, result.Changes, result.ErrorCode
	err = finishAudit(ticket, event)
	var body map[string]json.RawMessage
	if decodeErr := json.Unmarshal(response.Body, &body); decodeErr != nil || body == nil {
		return managementError(http.StatusInternalServerError, "invalid_management_response")
	}
	meta := map[string]any{"operation_id": ticket.ID, "recorded": err == nil}
	if err != nil {
		meta["error_code"] = "audit_write_failed"
	}
	body["audit"], _ = json.Marshal(meta)
	response.Body, _ = json.Marshal(body)
	return response
}

func auditedStateManagement(req pluginapi.ManagementRequest, handler func(pluginapi.ManagementRequest) pluginapi.ManagementResponse) pluginapi.ManagementResponse {
	return withAuditedManagement(req, func() (pluginapi.ManagementResponse, auditResult) {
		before, _ := loadedStateSnapshot()
		resp := handler(req)
		after, _ := loadedStateSnapshot()
		return resp, auditStateResult(req, before, after, resp)
	})
}

func auditRequestKey(req pluginapi.ManagementRequest) string {
	if req.Method != http.MethodPost {
		return keyFromRequest(req)
	}
	var body struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(req.Body, &body)
	return strings.TrimSpace(body.Key)
}

func auditKeyRef(key string) string {
	if key == "" {
		return "unspecified"
	}
	sum := sha256.Sum256([]byte(key))
	return "sha256:" + hex.EncodeToString(sum[:]) + " masked:***"
}

func auditEventForRequest(req pluginapi.ManagementRequest, before State) auditEvent {
	event := auditEvent{Actor: "management_api", Action: "update", ObjectType: "key_binding", ObjectRef: auditKeyRef(auditRequestKey(req))}
	if strings.TrimRight(req.Path, "/") == managementHandleBase+"/rules" {
		event.ObjectType, event.ObjectRef = "rules", "rules"
		return event
	}
	if strings.TrimRight(req.Path, "/") == managementHandleBase+"/keeper/auth-names" {
		var body struct {
			AuthIndex string `json:"auth_index"`
		}
		_ = json.Unmarshal(req.Body, &body)
		event.ObjectType, event.ObjectRef = "keeper_auth_name", strings.TrimSpace(body.AuthIndex)
		if event.ObjectRef == "" {
			event.ObjectRef = "unspecified"
		}
		return event
	}
	if req.Method == http.MethodDelete {
		event.Action = "delete"
	}
	if req.Method == http.MethodPost && auditBinding(before, auditRequestKey(req)) == nil {
		event.Action = "create"
	}
	return event
}

// Only known secret values are collected; neither raw headers nor body fields
// are ever included in the event. Callers supply business changes explicitly;
// the common wrapper sanitizes those changes, including Keeper name results.
func auditSecrets(req pluginapi.ManagementRequest, cfg Config, states ...State) []string {
	values := []string{auditRequestKey(req), os.Getenv(strings.TrimSpace(cfg.UsageKeeperPasswordEnv))}
	for _, st := range states {
		for _, binding := range st.KeyBindings {
			values = append(values, binding.Key)
		}
	}
	for name, entries := range req.Headers {
		switch strings.ToLower(name) {
		case "authorization", "proxy-authorization", "x-api-key", "x-management-key", "x-auth-token", "cookie":
			for _, value := range entries {
				values = append(values, value)
				if strings.EqualFold(name, "authorization") || strings.EqualFold(name, "proxy-authorization") {
					if _, token, found := strings.Cut(value, " "); found {
						values = append(values, strings.TrimSpace(token))
					}
				}
				if strings.EqualFold(name, "cookie") {
					request := http.Request{Header: http.Header{"Cookie": []string{value}}}
					for _, cookie := range request.Cookies() {
						values = append(values, cookie.Value)
					}
				}
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values
}

func auditRedactText(value string, secrets []string) string {
	pairs := make([]string, 0, len(secrets)*2)
	for _, secret := range secrets {
		if secret != "" {
			pairs = append(pairs, secret, "[REDACTED]")
		}
	}
	return strings.NewReplacer(pairs...).Replace(value)
}

func auditRedactJSON(raw json.RawMessage, secrets []string) json.RawMessage {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return json.RawMessage("null")
	}
	var redact func(any) any
	redact = func(value any) any {
		switch typed := value.(type) {
		case string:
			return auditRedactText(typed, secrets)
		case []any:
			for i, v := range typed {
				typed[i] = redact(v)
			}
		case map[string]any:
			for key, v := range typed {
				typed[key] = redact(v)
			}
		}
		return value
	}
	result, _ := json.Marshal(redact(value))
	return result
}

func auditBinding(st State, key string) *KeyBinding {
	for _, binding := range st.KeyBindings {
		if binding.Key == key {
			return &binding
		}
	}
	return nil
}

// Explicit projections avoid serializing State, request bodies or key values.
func auditBindingFields(binding *KeyBinding) map[string]any {
	if binding == nil {
		return map[string]any{}
	}
	return map[string]any{"alias": binding.Alias, "enabled": binding.Enabled, "blocked": binding.Blocked,
		"rules.global": binding.Rules.Global, "rules.claude": binding.Rules.Claude, "rules.codex": binding.Rules.Codex, "rules.openai": binding.Rules.OpenAI,
		"channel_target": binding.ChannelTarget, "fast_allowed": binding.FastAllowed}
}

func auditStateResult(req pluginapi.ManagementRequest, before, after State, resp pluginapi.ManagementResponse) auditResult {
	changed := false
	result := auditResult{Outcome: "failed", Changed: &changed}
	if resp.StatusCode >= 400 {
		switch resp.StatusCode {
		case 400:
			result.ErrorCode = "invalid_request"
		case 404:
			result.ErrorCode = "not_found"
		default:
			result.ErrorCode = "state_write_failed"
		}
		return result
	}
	result.Outcome = "succeeded"
	key := auditRequestKey(req)
	oldFields, newFields := auditBindingFields(auditBinding(before, key)), auditBindingFields(auditBinding(after, key))
	if strings.TrimRight(req.Path, "/") == managementHandleBase+"/rules" {
		oldFields = map[string]any{"rules.global": before.Rules.Global, "rules.claude": before.Rules.Claude, "rules.codex": before.Rules.Codex, "rules.openai": before.Rules.OpenAI}
		newFields = map[string]any{"rules.global": after.Rules.Global, "rules.claude": after.Rules.Claude, "rules.codex": after.Rules.Codex, "rules.openai": after.Rules.OpenAI}
	}
	result.Changes = map[string]auditChange{}
	fields := map[string]bool{}
	for field := range oldFields {
		fields[field] = true
	}
	for field := range newFields {
		fields[field] = true
	}
	for field := range fields {
		oldRaw, _ := json.Marshal(oldFields[field])
		newRaw, _ := json.Marshal(newFields[field])
		if bytes.Equal(oldRaw, newRaw) {
			continue
		}
		changed = true
		result.Changes[field] = auditChange{Before: oldRaw, After: newRaw}
	}
	return result
}
