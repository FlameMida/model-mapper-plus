package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func auditTestClock(t *testing.T, when string) *time.Time {
	t.Helper()
	now, err := time.Parse(time.RFC3339Nano, when)
	if err != nil {
		t.Fatal(err)
	}
	previous := auditNow
	auditNow = func() time.Time { return now }
	t.Cleanup(func() { auditNow = previous })
	return &now
}

func TestAuditS7ImmediatePersistenceAndMidnight(t *testing.T) {
	state := setupManagementTest(t, Config{Enabled: true})
	now := auditTestClock(t, "2026-09-12T15:59:59Z")
	ticket, err := beginAudit(state, auditEvent{Actor: "management_api", Action: "update", Module: "rules", ObjectType: "rules", ObjectRef: "rules"})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if ticket.ID == "" || filepath.Base(ticket.Path) != "2026-09-12.jsonl" {
		t.Fatalf("ticket: %+v", ticket)
	}
	raw, err := os.ReadFile(ticket.Path)
	if err != nil {
		t.Fatal(err)
	}
	var start auditEvent
	if err := json.Unmarshal(raw, &start); err != nil {
		t.Fatal(err)
	}
	if start.Phase != "start" || start.Version != 2 || start.Module != "rules" || start.OperationID != ticket.ID || !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("start: %s", raw)
	}
	for path, want := range map[string]os.FileMode{filepath.Dir(ticket.Path): 0700, ticket.Path: 0600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("mode %s: %o", path, info.Mode().Perm())
		}
	}
	running := auditQueryRequest(url.Values{"date": {"2026-09-12"}})
	if running.StatusCode != 200 || !strings.Contains(string(running.Body), `"outcome":"running"`) {
		t.Fatalf("running: %s", running.Body)
	}
	*now = now.Add(2 * time.Second)
	changed := true
	if err := finishAudit(ticket, auditEvent{Module: "rules", Outcome: "succeeded", Changed: &changed, Changes: map[string]auditChange{"global": {Before: json.RawMessage(`"old"`), After: json.RawMessage(`"new"`)}}}); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(ticket.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "\n") != 2 || !strings.Contains(string(raw), `2026-09-13T00:00:01+08:00`) {
		t.Fatalf("finish wrong day/time: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(ticket.Path), "2026-09-13.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("cross-day file: %v", err)
	}
	got := auditQueryRequest(url.Values{"date": {"2026-09-12"}})
	if got.StatusCode != 200 || !strings.Contains(string(got.Body), `"outcome":"succeeded"`) || !strings.Contains(string(got.Body), `"total":1`) {
		t.Fatalf("finish query: %s", got.Body)
	}
}

func TestAuditS7PreservesTruncatedTailAndTightensPermissions(t *testing.T) {
	state := setupManagementTest(t, Config{Enabled: true})
	auditTestClock(t, "2026-09-12T10:00:00+08:00")
	dir := filepath.Join(filepath.Dir(state), "model-mapper-plus-audit")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-09-12.jsonl")
	if err := os.WriteFile(path, []byte(`{"broken":`), 0644); err != nil {
		t.Fatal(err)
	}
	ticket, err := beginAudit(state, auditEvent{Actor: "management_api", Action: "update", Module: "rules", ObjectType: "rules", ObjectRef: "rules"})
	if err != nil {
		t.Fatal(err)
	}
	if err := finishAudit(ticket, auditEvent{Module: "rules", Outcome: "unknown"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "{\"broken\":\n{") || strings.Count(string(raw), "\n") != 3 {
		t.Fatalf("lost bad tail: %s", raw)
	}
	for _, p := range []string{dir, path} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0600)
		if p == dir {
			want = 0700
		}
		if info.Mode().Perm() != want {
			t.Fatalf("permissions %s %o", p, info.Mode().Perm())
		}
	}
	got := auditQueryRequest(url.Values{"date": {"2026-09-12"}})
	if got.StatusCode != 200 || !strings.Contains(string(got.Body), `"total":1`) || strings.Contains(string(got.Body), `"warnings":[]`) {
		t.Fatalf("bad tail query: %s", got.Body)
	}
}

type auditFaultFile struct {
	*os.File
	writeErr bool
	short    bool
	syncErr  bool
}

func (f auditFaultFile) Write(p []byte) (int, error) {
	if f.writeErr {
		return 0, errors.New("injected write failure")
	}
	if f.short {
		return f.File.Write(p[:len(p)/2])
	}
	return f.File.Write(p)
}
func (f auditFaultFile) Sync() error {
	if f.syncErr {
		return errors.New("injected sync failure")
	}
	return f.File.Sync()
}

func TestAuditS7WriteFailures(t *testing.T) {
	for _, mode := range []string{"write", "short", "sync"} {
		t.Run(mode, func(t *testing.T) {
			state := setupManagementTest(t, Config{Enabled: true})
			auditTestClock(t, "2026-09-12T10:00:00+08:00")
			old := auditOpenFile
			t.Cleanup(func() { auditOpenFile = old })
			auditOpenFile = func(path string, flag int, perm os.FileMode) (auditFile, error) {
				f, err := os.OpenFile(path, flag, perm)
				if err != nil {
					return nil, err
				}
				return auditFaultFile{File: f, writeErr: mode == "write", short: mode == "short", syncErr: mode == "sync"}, nil
			}
			if ticket, err := beginAudit(state, auditEvent{Actor: "management_api", Action: "update", Module: "rules", ObjectType: "rules", ObjectRef: "rules"}); err == nil || ticket.ID != "" {
				t.Fatalf("failed Begin accepted: %+v %v", ticket, err)
			}
			auditOpenFile = old
			ticket, err := beginAudit(state, auditEvent{Actor: "management_api", Action: "update", Module: "rules", ObjectType: "rules", ObjectRef: "rules"})
			if err != nil {
				t.Fatal(err)
			}
			auditOpenFile = func(path string, flag int, perm os.FileMode) (auditFile, error) {
				f, err := os.OpenFile(path, flag, perm)
				if err != nil {
					return nil, err
				}
				return auditFaultFile{File: f, writeErr: true}, nil
			}
			if err := finishAudit(ticket, auditEvent{Module: "rules", Outcome: "succeeded"}); err == nil {
				t.Fatal("finish accepted write failure")
			}
			got := auditQueryRequest(url.Values{"date": {"2026-09-12"}})
			if strings.Contains(string(got.Body), `"outcome":"running"`) || !strings.Contains(string(got.Body), `"outcome":"unknown"`) {
				t.Fatalf("failed finish remains active: %s", got.Body)
			}
		})
	}
}
