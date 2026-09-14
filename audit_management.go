package main

import (
	"bytes"
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
	startSecrets := auditSecrets(req, cfg, before)
	if event.ObjectType == "keeper_auth_name" {
		event.ObjectRef = auditRedactText(event.ObjectRef, startSecrets)
	}
	event.ObjectLabel = auditRedactText(event.ObjectLabel, startSecrets)
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
	// Final enrichment rides the finish record: creations only know their
	// alias after the mutation ran, and channel display names are attached to
	// the IDs that actually changed. The reader prefers finish over start.
	if event.Module == "key_binding" && event.ObjectLabel == "" {
		if binding := auditBinding(after, auditRequestKey(req)); binding != nil {
			event.ObjectLabel = binding.Alias
		}
	}
	if ids := auditChangedChannelIDs(result.Changes); len(ids) > 0 {
		for id, label := range auditChannelLabels(ids) {
			if event.Labels == nil {
				event.Labels = map[string]string{}
			}
			event.Labels[id] = label
		}
	}
	event.ObjectLabel = auditRedactText(event.ObjectLabel, secrets)
	for id, label := range event.Labels {
		event.Labels[id] = auditRedactText(label, secrets)
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

// auditKeyRef renders a stable, non-secret identifier: masked body plus the
// trailing four characters, mirroring the credential label convention. Keys
// of four characters or fewer stay fully masked.
func auditKeyRef(key string) string {
	if key == "" {
		return "key:unspecified"
	}
	if len(key) <= 4 {
		return "key:••••"
	}
	return "key:••••" + key[len(key)-4:]
}

func auditBodyAlias(body []byte) string {
	var parsed struct {
		Alias string `json:"alias"`
	}
	_ = json.Unmarshal(body, &parsed)
	return strings.TrimSpace(parsed.Alias)
}

// auditNotificationName finds a key-level notification's display name so
// test-send operations can name their target; the global default has none.
func auditNotificationName(st State, id string) string {
	for _, binding := range st.KeyBindings {
		for _, notification := range binding.Notifications {
			if notification.ID == id {
				return notification.Name
			}
		}
	}
	return ""
}

func auditEventForRequest(req pluginapi.ManagementRequest, before State) auditEvent {
	event := auditEvent{Actor: "management_api", Action: "update", ObjectType: "key_binding", ObjectRef: "unspecified", Module: "other"}
	path := managementRequestPath(req.Path)
	switch {
	case path == managementHandleBase+"/rules":
		event.Module, event.ObjectType, event.ObjectRef, event.ObjectLabel = "rules", "rules", "rules", "全局规则"
		return event
	case path == managementHandleBase+"/keeper/auth-names":
		var body struct {
			AuthIndex string `json:"auth_index"`
			Alias     string `json:"alias"`
		}
		_ = json.Unmarshal(req.Body, &body)
		event.Module, event.ObjectType = "keeper_auth_name", "keeper_auth_name"
		event.ObjectRef = strings.TrimSpace(body.AuthIndex)
		if event.ObjectRef == "" {
			event.ObjectRef = "unspecified"
		}
		event.ObjectLabel = strings.TrimSpace(body.Alias)
		return event
	case path == notificationHandleBase+"/settings" && req.Method == http.MethodPut:
		event.Module, event.ObjectType, event.ObjectRef, event.ObjectLabel = "notifications", "notification_settings", "global", "全局通知设置"
		return event
	case path == notificationHandleBase+"/test-send" && req.Method == http.MethodPost:
		var body struct {
			Key            string `json:"key"`
			NotificationID string `json:"notification_id"`
		}
		_ = json.Unmarshal(req.Body, &body)
		event.Module, event.Action, event.ObjectType = "notifications", "test_send", "notification_test"
		switch {
		case strings.TrimSpace(body.Key) != "":
			key := strings.TrimSpace(body.Key)
			event.ObjectRef = auditKeyRef(key)
			if binding := auditBinding(before, key); binding != nil {
				event.ObjectLabel = binding.Alias
			}
		case strings.TrimSpace(body.NotificationID) != "":
			id := strings.TrimSpace(body.NotificationID)
			event.ObjectRef = "notification:" + id
			event.ObjectLabel = auditNotificationName(before, id)
		default:
			event.ObjectRef, event.ObjectLabel = "notification:default", "默认通知"
		}
		return event
	}
	event.Module = "key_binding"
	key := auditRequestKey(req)
	event.ObjectRef = auditKeyRef(key)
	// POST carries the incoming alias (an upsert shows the name it writes);
	// other methods snapshot the binding's alias at operation time.
	alias := ""
	if req.Method == http.MethodPost {
		alias = auditBodyAlias(req.Body)
	}
	if alias == "" {
		if binding := auditBinding(before, key); binding != nil {
			alias = binding.Alias
		} else {
			alias = auditBodyAlias(req.Body)
		}
	}
	event.ObjectLabel = alias
	if req.Method == http.MethodDelete {
		event.Action = "delete"
	}
	if req.Method == http.MethodPost && auditBinding(before, key) == nil {
		event.Action = "create"
	}
	return event
}

// Only known secret values are collected; neither raw headers nor body fields
// are ever included in the event. Callers supply business changes explicitly;
// the common wrapper sanitizes those changes, including Keeper name results.
// Notification webhooks and signature secrets join the known values so the
// settings diff can show that they changed without echoing them.
func auditSecrets(req pluginapi.ManagementRequest, cfg Config, states ...State) []string {
	values := []string{auditRequestKey(req), os.Getenv(strings.TrimSpace(cfg.UsageKeeperPasswordEnv))}
	for _, st := range states {
		for _, binding := range st.KeyBindings {
			values = append(values, binding.Key)
			collectNotificationSecrets(&values, binding.Notifications)
		}
		collectNotificationSecrets(&values, notificationList(st))
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

func notificationList(st State) []Notification {
	if st.Notifications == nil {
		return nil
	}
	// The whole global list feeds secret collection; the synced global_default
	// entry is covered by the same values.
	if len(st.Notifications.Notifications) > 0 {
		return st.Notifications.Notifications
	}
	return []Notification{st.Notifications.GlobalDefault}
}

func collectNotificationSecrets(values *[]string, notifications []Notification) {
	for _, notification := range notifications {
		for _, platform := range notification.Platforms {
			for _, value := range []string{
				platform.Webhook,
				platform.SignSecret,
				platform.FetchAppID,
				platform.FetchAppSecret,
				platform.FetchAppKey,
				platform.FetchCorpID,
				platform.FetchSecret,
			} {
				if trimmed := strings.TrimSpace(value); trimmed != "" {
					*values = append(*values, trimmed)
				}
			}
		}
	}
}

// auditChangedChannelIDs collects every supplier/auth ID referenced by a
// channel diff so their display names can be snapshotted onto the event.
func auditChangedChannelIDs(changes map[string]auditChange) []string {
	var ids []string
	for _, field := range []string{"channel_target.suppliers", "channel_target.auth_ids"} {
		change, exists := changes[field]
		if !exists {
			continue
		}
		for _, raw := range []json.RawMessage{change.Before, change.After} {
			var list []string
			if json.Unmarshal(raw, &list) == nil {
				ids = append(ids, list...)
			}
		}
	}
	return ids
}

func auditNotificationSummaries(notifications []Notification) any {
	if notifications == nil {
		return nil
	}
	rows := make([]map[string]any, 0, len(notifications))
	for _, notification := range notifications {
		rows = append(rows, map[string]any{"id": notification.ID, "name": notification.Name, "enabled": notification.Enabled})
	}
	return rows
}

// Explicit projections avoid serializing State, request bodies or key values.
// The channel target is split into its three editable fields so each shows its
// own before/after pair instead of one opaque JSON blob.
func auditBindingFields(binding *KeyBinding) map[string]any {
	if binding == nil {
		return map[string]any{}
	}
	fields := map[string]any{"alias": binding.Alias, "enabled": binding.Enabled, "blocked": binding.Blocked,
		"rules.global": binding.Rules.Global, "rules.claude": binding.Rules.Claude, "rules.codex": binding.Rules.Codex, "rules.openai": binding.Rules.OpenAI,
		"notifications": auditNotificationSummaries(binding.Notifications), "fast_allowed": binding.FastAllowed}
	if binding.ChannelTarget != nil {
		fields["channel_target.enabled"] = binding.ChannelTarget.Enabled
		fields["channel_target.suppliers"] = binding.ChannelTarget.Suppliers
		fields["channel_target.auth_ids"] = binding.ChannelTarget.AuthIDs
	} else {
		fields["channel_target.enabled"] = nil
		fields["channel_target.suppliers"] = nil
		fields["channel_target.auth_ids"] = nil
	}
	return fields
}

func auditNotificationSettingsFields(settings *NotificationSettings) map[string]any {
	// The multi-entry global list is the source of truth; global_default stays
	// projected for continuity with pre-v0.6.0 records.
	fields := map[string]any{"notifications.enabled": nil,
		"notifications.list":                     nil,
		"notifications.global_default.platforms": nil}
	if settings != nil {
		fields["notifications.enabled"] = settings.Enabled
		fields["notifications.list"] = settings.Notifications
		fields["notifications.global_default.platforms"] = settings.GlobalDefault.Platforms
	}
	return fields
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
	switch managementRequestPath(req.Path) {
	case managementHandleBase + "/rules":
		oldFields = map[string]any{"rules.global": before.Rules.Global, "rules.claude": before.Rules.Claude, "rules.codex": before.Rules.Codex, "rules.openai": before.Rules.OpenAI}
		newFields = map[string]any{"rules.global": after.Rules.Global, "rules.claude": after.Rules.Claude, "rules.codex": after.Rules.Codex, "rules.openai": after.Rules.OpenAI}
	case notificationHandleBase + "/settings":
		oldFields, newFields = auditNotificationSettingsFields(before.Notifications), auditNotificationSettingsFields(after.Notifications)
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
