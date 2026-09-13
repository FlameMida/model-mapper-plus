package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const auditNotificationSettingsBody = `{"enabled":true,"global_default":{"id":"global","name":"用量通知","enabled":true,
  "modules":[{"kind":"daily","period":"current"}],
  "schedule":{"kind":"interval","interval":86400,"time":"09:00:00"},
  "platforms":[{"kind":"feishu","enabled":true,"webhook":"https://open.feishu.cn/hook/s3cr3t","user_ids":["ou_a"],"sign_secret":"sec1"}]}}`

// Scenario S1/S5: notification settings land in the notifications module with
// per-field projections, and webhook/sign-secret values stay masked while the
// changed field remains visible in the diff.
func TestManagementAuditNotificationSettingsProjection(t *testing.T) {
	statePath := setupManagementTest(t, Config{Enabled: true})
	resp := dispatchManagement(auditRequest(http.MethodPut, "/notifications/settings", auditNotificationSettingsBody))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put: %d %s", resp.StatusCode, resp.Body)
	}
	page := auditPageForTest(t)
	if page.Total != 1 {
		t.Fatalf("page=%+v", page)
	}
	item := page.Items[0]
	if item.Module != "notifications" || item.ObjectType != "notification_settings" || item.ObjectRef != "global" || item.ObjectLabel != "全局通知设置" {
		t.Fatalf("item=%+v", item)
	}
	for _, field := range []string{"notifications.enabled", "notifications.global_default.enabled",
		"notifications.global_default.modules", "notifications.global_default.schedule", "notifications.global_default.platforms"} {
		if _, exists := item.Changes[field]; !exists {
			t.Fatalf("missing projection %s: %+v", field, item.Changes)
		}
	}
	if string(item.Changes["notifications.enabled"].After) != "true" {
		t.Fatalf("enabled diff=%+v", item.Changes["notifications.enabled"])
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(statePath), "model-mapper-plus-audit", "*.jsonl"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	rawPage, _ := json.Marshal(page)
	for _, secret := range []string{"s3cr3t", "sec1"} {
		if bytes.Contains(raw, []byte(secret)) || bytes.Contains(rawPage, []byte(secret)) {
			t.Errorf("notification secret leaked: %s", secret)
		}
	}
	if !bytes.Contains(rawPage, []byte("[REDACTED]")) {
		t.Errorf("platform diff lost under redaction: %s", item.Changes["notifications.global_default.platforms"])
	}
	// A changed webhook still appears as a changed field, masked on both sides.
	changed := strings.Replace(auditNotificationSettingsBody, "hook/s3cr3t", "hook/other9", 1)
	resp = dispatchManagement(auditRequest(http.MethodPut, "/notifications/settings", changed))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second put: %d %s", resp.StatusCode, resp.Body)
	}
	page = auditPageForTest(t)
	if page.Total != 2 {
		t.Fatalf("page=%+v", page)
	}
	second := page.Items[0]
	if _, exists := second.Changes["notifications.global_default.platforms"]; !exists {
		t.Fatalf("webhook change invisible: %+v", second.Changes)
	}
	if second.Changed == nil || !*second.Changed {
		t.Fatalf("changed flag=%v", second.Changed)
	}
}

// Scenario S1: test-send records a valid finish event (changed=false) in the
// notifications module; the previous Changed=nil shape was dropped as invalid.
func TestManagementAuditNotificationTestSend(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	resp := dispatchManagement(auditRequest(http.MethodPost, "/notifications/test-send", `{"notification_id":"missing"}`))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("test-send: %d %s", resp.StatusCode, resp.Body)
	}
	page := auditPageForTest(t)
	if page.Total != 1 {
		t.Fatalf("page=%+v", page)
	}
	item := page.Items[0]
	if item.Module != "notifications" || item.Action != "test_send" || item.ObjectType != "notification_test" || item.ObjectRef != "notification:missing" {
		t.Fatalf("item=%+v", item)
	}
	if item.Outcome != "failed" || item.ErrorCode != "not_found" {
		t.Fatalf("outcome=%+v", item)
	}
	if item.Changed == nil || *item.Changed || len(item.Changes) != 0 {
		t.Fatalf("changed=%+v changes=%+v", item.Changed, item.Changes)
	}
	if len(page.Warnings) != 0 {
		t.Fatalf("finish judged invalid: %v", page.Warnings)
	}
	// Default-target shape when neither key nor notification_id is supplied.
	resp = dispatchManagement(auditRequest(http.MethodPost, "/notifications/test-send", `{}`))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("default test-send: %d %s", resp.StatusCode, resp.Body)
	}
	page = auditPageForTest(t)
	defaultItem := page.Items[0]
	if defaultItem.ObjectRef != "notification:default" || defaultItem.ObjectLabel != "默认通知" {
		t.Fatalf("default item=%+v", defaultItem)
	}
}
