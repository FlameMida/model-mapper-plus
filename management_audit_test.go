package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func auditRequest(method, path, body string) pluginapi.ManagementRequest {
	return pluginapi.ManagementRequest{Method: method, Path: managementHandleBase + path, Body: []byte(body)}
}

type managementAuditFaultFile struct {
	*os.File
	failWrite, failSync bool
	writeGate           func()
}

func (f *managementAuditFaultFile) Write(raw []byte) (int, error) {
	if f.writeGate != nil {
		f.writeGate()
	}
	if f.failWrite {
		return 0, errors.New("injected audit write failure")
	}
	return f.File.Write(raw)
}

func (f *managementAuditFaultFile) Sync() error {
	if f.failSync {
		return errors.New("injected audit sync failure")
	}
	return f.File.Sync()
}

func TestManagementAuditWriteAndSyncFaults(t *testing.T) {
	for _, phase := range []string{"begin", "finish"} {
		for _, fault := range []string{"write", "sync"} {
			t.Run(phase+"-"+fault, func(t *testing.T) {
				statePath := setupManagementTest(t, Config{Enabled: true})
				original := auditOpenFile
				t.Cleanup(func() { auditOpenFile = original })
				calls := 0
				auditOpenFile = func(path string, flag int, perm os.FileMode) (auditFile, error) {
					file, err := os.OpenFile(path, flag, perm)
					if err != nil {
						return nil, err
					}
					calls++
					fail := calls == 1 && phase == "begin" || calls == 2 && phase == "finish"
					return &managementAuditFaultFile{File: file, failWrite: fail && fault == "write", failSync: fail && fault == "sync"}, nil
				}
				resp := dispatchManagement(auditRequest(http.MethodPost, "/keys", `{"key":"sk-fault","alias":"A","enabled":true}`))
				if phase == "begin" {
					if resp.StatusCode != 503 {
						t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
					}
					if _, err := os.Stat(statePath); !os.IsNotExist(err) {
						t.Fatalf("state changed: %v", err)
					}
					st, _ := loadedStateSnapshot()
					if len(st.KeyBindings) != 0 {
						t.Fatal("memory changed")
					}
				} else {
					if resp.StatusCode != 200 {
						t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
					}
					var body struct {
						Audit struct {
							Recorded  bool   `json:"recorded"`
							ErrorCode string `json:"error_code"`
						} `json:"audit"`
					}
					decodeBody(t, resp, &body)
					if body.Audit.Recorded || body.Audit.ErrorCode != "audit_write_failed" {
						t.Fatalf("missing warning: %s", resp.Body)
					}
					st, err := readStateFile(statePath)
					if err != nil || len(st.KeyBindings) != 1 {
						t.Fatalf("business state=%+v err=%v", st, err)
					}
					page := auditPageForTest(t)
					if page.Total != 1 || page.Items[0].Outcome != "unknown" {
						t.Fatalf("query=%+v", page)
					}
				}
				// A later submission independently passes the real Begin gate.
				auditOpenFile = original
				resp = dispatchManagement(auditRequest(http.MethodPost, "/keys", `{"key":"sk-next","alias":"B","enabled":true}`))
				if resp.StatusCode != 200 {
					t.Fatalf("next=%s", resp.Body)
				}
				auditMetaForTest(t, resp)
			})
		}
	}
}

func TestManagementAuditRedactsKnownSecrets(t *testing.T) {
	t.Setenv("T02_KEEPER_PASSWORD", "keeper-secret-123")
	statePath := setupManagementTest(t, Config{Enabled: true, UsageKeeperPasswordEnv: "T02_KEEPER_PASSWORD"})
	req := auditRequest(http.MethodPost, "/keys", `{"key":"sk-known-secret","alias":"sk-known-secret keeper-secret-123 cpa-secret-123 cookie-secret-123","enabled":true,"rules":{"global":"sk-known-secret=>keeper-secret-123"}}`)
	req.Headers = http.Header{"Authorization": []string{"Bearer cpa-secret-123"}, "Cookie": []string{"session=cookie-secret-123"}, "X-Actor": []string{"pretend-admin"}}
	resp := dispatchManagement(req)
	if resp.StatusCode != 200 {
		t.Fatalf("save=%s", resp.Body)
	}
	page := auditPageForTest(t)
	rawPage, _ := json.Marshal(page)
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(statePath), "model-mapper-plus-audit", "*.jsonl"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"sk-known-secret", "keeper-secret-123", "cpa-secret-123", "cookie-secret-123", "pretend-admin"} {
		if bytes.Contains(raw, []byte(secret)) || bytes.Contains(rawPage, []byte(secret)) {
			t.Errorf("secret leaked: %s", secret)
		}
	}
	if page.Total != 1 || !strings.Contains(page.Items[0].ObjectRef, "sha256:") || !strings.Contains(page.Items[0].ObjectRef, "***") {
		t.Fatalf("object ref=%+v", page)
	}
	// Changes between secrets must remain changed even if projections mask equally.
	req = auditRequest(http.MethodPatch, "/keys", `{"key":"sk-known-secret","alias":"keeper-secret-123"}`)
	resp = dispatchManagement(req)
	auditMetaForTest(t, resp)
}

func TestManagementAuditShortSecretsKeepFingerprintAndMask(t *testing.T) {
	t.Setenv("T02_SHORT_SECRET", "A")
	setupManagementTest(t, Config{Enabled: true, UsageKeeperPasswordEnv: "T02_SHORT_SECRET"})
	req := auditRequest(http.MethodPost, "/keys", `{"key":"a","alias":"A","enabled":true}`)
	req.Headers = http.Header{"Authorization": []string{"Bearer A"}}
	resp := dispatchManagement(req)
	if resp.StatusCode != 200 {
		t.Fatalf("save=%s", resp.Body)
	}
	page := auditPageForTest(t)
	if page.Total != 1 {
		t.Fatalf("page=%+v", page)
	}
	if page.Items[0].ObjectRef != "sha256:ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb masked:***" {
		t.Errorf("fingerprint changed: %s", page.Items[0].ObjectRef)
	}
	var alias string
	if err := json.Unmarshal(page.Items[0].Changes["alias"].After, &alias); err != nil {
		t.Fatal(err)
	}
	if alias != "[REDACTED]" {
		t.Fatalf("mask=%q", alias)
	}
}

func TestManagementAuditReconfigureKeepsOperationSnapshot(t *testing.T) {
	statePath := setupManagementTest(t, Config{Enabled: true})
	newPath := filepath.Join(t.TempDir(), "new-state.json")
	entered, release := make(chan struct{}), make(chan struct{})
	original := auditOpenFile
	t.Cleanup(func() { auditOpenFile = original })
	auditOpenFile = func(path string, flag int, perm os.FileMode) (auditFile, error) {
		file, err := os.OpenFile(path, flag, perm)
		if err != nil {
			return nil, err
		}
		return &managementAuditFaultFile{File: file, writeGate: func() {
			select {
			case <-entered:
			default:
				close(entered)
				<-release
			}
		}}, nil
	}
	saved := make(chan pluginapi.ManagementResponse, 1)
	go func() {
		saved <- dispatchManagement(auditRequest(http.MethodPost, "/keys", `{"key":"sk-snapshot","enabled":true}`))
	}()
	<-entered
	reconfigured := make(chan error, 1)
	go func() {
		raw, _ := json.Marshal(Config{Enabled: true, StateFile: newPath})
		_, err := handlePluginReconfigure(raw)
		reconfigured <- err
	}()
	var early bool
	select {
	case err := <-reconfigured:
		early = true
		if err != nil {
			t.Error(err)
		}
	case <-time.After(100 * time.Millisecond):
	}
	readDone := make(chan struct{})
	go func() { _ = loadedConfig(); _ = loadedRuleSource(); close(readDone) }()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Error("snapshot reads blocked by management IO")
	}
	close(release)
	resp := <-saved
	if resp.StatusCode != 200 {
		t.Fatalf("save=%s", resp.Body)
	}
	if !early {
		if err := <-reconfigured; err != nil {
			t.Fatal(err)
		}
	}
	if early {
		t.Error("reconfigure crossed active mutation")
	}
	old, err := readStateFile(statePath)
	if err != nil || len(old.KeyBindings) != 1 {
		t.Errorf("old path state=%+v err=%v", old, err)
	}
	newState, err := readStateFile(newPath)
	if err != nil || len(newState.KeyBindings) != 1 {
		t.Errorf("new path state=%+v err=%v", newState, err)
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(statePath), "model-mapper-plus-audit", "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("old logs=%v", files)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil || bytes.Count(raw, []byte{'\n'}) != 2 {
		t.Fatalf("operation split: %s %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(newPath), "model-mapper-plus-audit")); !os.IsNotExist(err) {
		t.Fatalf("reconfigure created logs: %v", err)
	}
}

func auditPageForTest(t *testing.T) auditPage {
	t.Helper()
	resp := dispatchManagement(auditRequest(http.MethodGet, "/audit", ""))
	if resp.StatusCode != 200 {
		t.Fatalf("audit query: %d %s", resp.StatusCode, resp.Body)
	}
	var page auditPage
	decodeBody(t, resp, &page)
	return page
}

func auditMetaForTest(t *testing.T, resp pluginapi.ManagementResponse) string {
	t.Helper()
	var body struct {
		Audit struct {
			OperationID string `json:"operation_id"`
			Recorded    bool   `json:"recorded"`
		} `json:"audit"`
	}
	decodeBody(t, resp, &body)
	if body.Audit.OperationID == "" || !body.Audit.Recorded {
		t.Fatalf("missing recorded audit: %s", resp.Body)
	}
	return body.Audit.OperationID
}

func TestManagementAuditS8BlocksBeforeMutation(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			statePath := setupManagementTest(t, Config{Enabled: true})
			var oldDisk []byte
			if method == http.MethodPatch || method == http.MethodDelete {
				seed := managementPostKey(auditRequest(http.MethodPost, "/keys", `{"key":"sk-secret","alias":"Before","enabled":false}`))
				if seed.StatusCode != 200 {
					t.Fatalf("seed=%s", seed.Body)
				}
				var err error
				oldDisk, err = os.ReadFile(statePath)
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := loadedStateSnapshot()
			if err := os.WriteFile(filepath.Join(filepath.Dir(statePath), "model-mapper-plus-audit"), []byte("not a directory"), 0600); err != nil {
				t.Fatal(err)
			}
			path, body := "/keys", `{"key":"sk-secret","alias":"A","enabled":true}`
			if method == http.MethodPut {
				path, body = "/rules", `{"global":"a=>b"}`
			}
			got := dispatchManagement(auditRequest(method, path, body))
			if got.StatusCode != 503 || !bytes.Contains(got.Body, []byte("audit_unavailable")) {
				t.Errorf("status=%d body=%s", got.StatusCode, got.Body)
			}
			if oldDisk == nil {
				if _, err := os.Stat(statePath); !os.IsNotExist(err) {
					t.Errorf("state changed: %v", err)
				}
			} else {
				gotDisk, err := os.ReadFile(statePath)
				if err != nil || !bytes.Equal(oldDisk, gotDisk) {
					t.Errorf("persisted state changed: %v", err)
				}
			}
			after, _ := loadedStateSnapshot()
			if !reflect.DeepEqual(before, after) {
				t.Error("in-memory state changed")
			}
		})
	}
}

func TestManagementAuditCRUDAndNoChange(t *testing.T) {
	statePath := setupManagementTest(t, Config{Enabled: true})
	steps := []struct {
		method, body, action string
		changed              bool
		status               int
	}{
		{http.MethodPost, `{"key":"sk-audit-secret","alias":"A","enabled":true}`, "create", true, 200},
		{http.MethodPost, `{"key":"sk-audit-secret","alias":"B","enabled":true}`, "update", true, 200},
		{http.MethodPost, `{"key":"sk-audit-secret","alias":"B","enabled":true}`, "update", false, 200},
		{http.MethodPatch, `{"key":"sk-audit-secret","enabled":false}`, "update", true, 200},
		{http.MethodDelete, `{"key":"sk-audit-secret"}`, "delete", true, 200},
		{http.MethodDelete, `{"key":"sk-audit-secret"}`, "delete", false, 404},
	}
	for i, step := range steps {
		resp := dispatchManagement(auditRequest(step.method, "/keys", step.body))
		if resp.StatusCode != step.status {
			t.Fatalf("step %d status=%d body=%s", i, resp.StatusCode, resp.Body)
		}
		id := auditMetaForTest(t, resp)
		page := auditPageForTest(t)
		if page.Total != i+1 {
			t.Fatalf("step %d total=%d", i, page.Total)
		}
		var item auditItem
		for _, candidate := range page.Items {
			if candidate.OperationID == id {
				item = candidate
			}
		}
		outcome := "succeeded"
		if step.status != 200 {
			outcome = "failed"
		}
		if item.Action != step.action || item.Outcome != outcome || item.Changed == nil || *item.Changed != step.changed {
			t.Fatalf("step %d item=%+v", i, item)
		}
		if item.Actor != "management_api" || item.ObjectType != "key_binding" || item.ObjectRef == "" {
			t.Fatalf("identity=%+v", item)
		}
		if step.changed && len(item.Changes) == 0 {
			t.Fatal("missing real diff")
		}
		if _, exists := item.Changes["channel_target"]; exists {
			t.Fatal("unchanged null channel target included in diff")
		}
		if _, exists := item.Changes["fast_allowed"]; exists {
			t.Fatal("unchanged null fast flag included in diff")
		}
		if !step.changed && len(item.Changes) != 0 {
			t.Fatal("fabricated diff")
		}
	}
	st, err := readStateFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.KeyBindings) != 0 {
		t.Fatalf("state=%+v", st)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(statePath), "model-mapper-plus-audit", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("files=%v err=%v", files, err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(raw, []byte{'\n'}) != 12 || bytes.Contains(raw, []byte("sk-audit-secret")) {
		t.Fatalf("invalid audit file: %s", raw)
	}
}

func TestManagementAuditRulesAndValidation(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	resp := dispatchManagement(auditRequest(http.MethodPut, "/rules", `{"global":"a=>b","claude":"c=>d","codex":"e=>f","openai":"g=>h"}`))
	if resp.StatusCode != 200 {
		t.Fatalf("rules: %s", resp.Body)
	}
	auditMetaForTest(t, resp)
	page := auditPageForTest(t)
	if page.Total != 1 || len(page.Items[0].Changes) != 4 {
		t.Fatalf("rules diff=%+v", page)
	}
	change := page.Items[0].Changes["rules.global"]
	var oldRule, newRule string
	if err := json.Unmarshal(change.Before, &oldRule); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(change.After, &newRule); err != nil {
		t.Fatal(err)
	}
	if oldRule != "" || newRule != "a=>b" {
		t.Fatalf("global diff=%+v", change)
	}
	before, _ := loadedStateSnapshot()
	for _, req := range []pluginapi.ManagementRequest{
		auditRequest(http.MethodPut, "/rules", `{`), auditRequest(http.MethodPost, "/keys", `{`),
		auditRequest(http.MethodPatch, "/keys", `{`), auditRequest(http.MethodDelete, "/keys", `{`),
		auditRequest(http.MethodPut, "/rules", `{"global":"gpt-* => deepseek"}`),
		auditRequest(http.MethodPatch, "/keys", `{"key":"absent","alias":"A"}`),
	} {
		resp := dispatchManagement(req)
		if resp.StatusCode < 400 {
			t.Fatalf("failure accepted: %s", resp.Body)
		}
		auditMetaForTest(t, resp)
	}
	after, _ := loadedStateSnapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("validation changed state")
	}
	page = auditPageForTest(t)
	if page.Total != 7 {
		t.Fatalf("total=%d", page.Total)
	}
	for _, item := range page.Items {
		if item.Outcome == "failed" && (item.Changed == nil || *item.Changed || len(item.Changes) != 0 || item.ErrorCode == "") {
			t.Fatalf("failure=%+v", item)
		}
	}
	for _, req := range []pluginapi.ManagementRequest{auditRequest(http.MethodGet, "/state", ""), auditRequest(http.MethodPost, "/preview", `{"format":"openai","model":"a"}`), auditRequest(http.MethodPost, "/channel-credentials", `{}`), auditRequest(http.MethodPost, "/keeper/key-aliases/refresh", `{}`)} {
		dispatchManagement(req)
	}
	if auditPageForTest(t).Total != 7 {
		t.Fatal("read-only calls audited")
	}
	// The audit is a separate journal; it must not become a state queue.
	raw, _ := json.Marshal(after)
	if bytes.Contains(raw, []byte("audit")) {
		t.Fatal("state contains audit")
	}
}
