package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestAuditQueryS10RejectsInvalidDate(t *testing.T) {
	setupManagementTest(t, Config{Enabled: true})
	for _, date := range []string{"../state", "2026-02-30", "2026-9-1"} {
		got := auditQueryRequest(url.Values{"date": {date}})
		if got.StatusCode != http.StatusBadRequest {
			t.Fatalf("date %q: status=%d body=%s", date, got.StatusCode, got.Body)
		}
	}
}

func auditQueryRequest(query url.Values) pluginapi.ManagementResponse {
	return dispatchManagement(pluginapi.ManagementRequest{Method: http.MethodGet, Path: managementHandleBase + "/audit", Query: query})
}

func TestAuditQueryS10PaginationAndDamagedLines(t *testing.T) {
	state := setupManagementTest(t, Config{Enabled: true})
	dir := filepath.Join(filepath.Dir(state), "model-mapper-plus-audit")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	fixture := `{"version":1,"operation_id":"a","phase":"start","occurred_at":"2026-09-12T10:00:00+08:00","actor":"management_api","action":"update","object_type":"rules","object_ref":"rules"}
{"version":1,"operation_id":"a","phase":"finish","occurred_at":"2026-09-12T10:00:01+08:00","outcome":"succeeded","changed":true,"changes":{"global":{"before":"a=>b","after":"a=>c"}}}
{"version":1,"operation_id":"b","phase":"start","occurred_at":"2026-09-12T10:00:00+08:00","actor":"management_api","action":"delete","object_type":"key_binding","object_ref":"masked"}
{broken
{"version":1,"operation_id":"tail"`
	if err := os.WriteFile(filepath.Join(dir, "2026-09-12.jsonl"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ page, id, outcome string }{{"1", "b", "unknown"}, {"2", "a", "succeeded"}} {
		got := auditQueryRequest(url.Values{"date": {"2026-09-12"}, "page": {tc.page}, "page_size": {"1"}})
		if got.StatusCode != 200 {
			t.Fatalf("response %d %s", got.StatusCode, got.Body)
		}
		var page struct {
			Total int
			Items []struct {
				OperationID string `json:"operation_id"`
				Outcome     string
				Changes     map[string]struct{ Before, After string }
			}
			Warnings []string
		}
		if err := json.Unmarshal(got.Body, &page); err != nil {
			t.Fatal(err)
		}
		if page.Total != 2 || len(page.Items) != 1 || page.Items[0].OperationID != tc.id || page.Items[0].Outcome != tc.outcome || len(page.Warnings) != 2 {
			t.Fatalf("unexpected page: %s", got.Body)
		}
		if tc.id == "a" && page.Items[0].Changes["global"].After != "a=>c" {
			t.Fatalf("lost changes: %s", got.Body)
		}
		if got.Headers.Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
	}
	for _, q := range []url.Values{{"page": {"0"}}, {"page": {"x"}}, {"page_size": {"0"}}, {"page_size": {"101"}}, {"page": {"999999999999999999999"}}} {
		if got := auditQueryRequest(q); got.StatusCode != 400 {
			t.Fatalf("query %v: %d", q, got.StatusCode)
		}
	}
	got := auditQueryRequest(url.Values{"date": {"2026-09-11"}})
	if got.StatusCode != 200 || !strings.Contains(string(got.Body), `"items":[]`) || !strings.Contains(string(got.Body), `"warnings":[]`) {
		t.Fatalf("empty day: %d %s", got.StatusCode, got.Body)
	}
}

func TestAuditQueryS10ReadErrorAndInvalidEvents(t *testing.T) {
	state := setupManagementTest(t, Config{Enabled: true})
	dir := filepath.Join(filepath.Dir(state), "model-mapper-plus-audit")
	if err := os.MkdirAll(filepath.Join(dir, "2026-09-12.jsonl"), 0700); err != nil {
		t.Fatal(err)
	}
	got := auditQueryRequest(url.Values{"date": {"2026-09-12"}})
	if got.StatusCode != 503 || strings.Contains(string(got.Body), dir) {
		t.Fatalf("read error: %d %s", got.StatusCode, got.Body)
	}
	start := `{"version":1,"operation_id":"a","phase":"start","occurred_at":"2026-09-11T10:00:00+08:00","actor":"management_api","action":"update","object_type":"rules","object_ref":"rules"}`
	lines := []string{start, start, `{"version":2}`, `{"version":1,"phase":"other"}`, `{"version":1,"operation_id":"orphan","phase":"finish","occurred_at":"2026-09-11T10:00:01+08:00","outcome":"failed","changed":false}`, `{"version":1,"operation_id":"bad","phase":"start","occurred_at":"garbage"}`}
	if err := os.WriteFile(filepath.Join(dir, "2026-09-11.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got = auditQueryRequest(url.Values{"date": {"2026-09-11"}})
	var result struct {
		Total    int
		Warnings []string
	}
	if err := json.Unmarshal(got.Body, &result); err != nil {
		t.Fatal(err)
	}
	if got.StatusCode != 200 || result.Total != 1 || len(result.Warnings) != 5 {
		t.Fatalf("invalid events: %d %s", got.StatusCode, got.Body)
	}
}

func TestAuditQueryMixedVersionsAndModuleFilter(t *testing.T) {
	state := setupManagementTest(t, Config{Enabled: true})
	dir := filepath.Join(filepath.Dir(state), "model-mapper-plus-audit")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// One v1 pair (legacy rules op) and two v2 pairs (key binding + rules).
	fixture := `{"version":1,"operation_id":"v1","phase":"start","occurred_at":"2026-09-12T09:00:00+08:00","actor":"management_api","action":"update","object_type":"rules","object_ref":"rules"}
{"version":1,"operation_id":"v1","phase":"finish","occurred_at":"2026-09-12T09:00:01+08:00","outcome":"succeeded","changed":true,"changes":{"rules.global":{"before":"a","after":"b"}}}
{"version":2,"operation_id":"k1","phase":"start","occurred_at":"2026-09-12T10:00:00+08:00","actor":"management_api","action":"update","module":"key_binding","object_type":"key_binding","object_ref":"key:••••8f2a","object_label":"prod-deepseek"}
{"version":2,"operation_id":"k1","phase":"finish","occurred_at":"2026-09-12T10:00:02+08:00","outcome":"succeeded","changed":true,"module":"key_binding","changes":{"channel_target.suppliers":{"before":["old"],"after":["new"]}},"labels":{"new":"火山方舟"}}
{"version":2,"operation_id":"r2","phase":"start","occurred_at":"2026-09-12T11:00:00+08:00","actor":"management_api","action":"update","module":"rules","object_type":"rules","object_ref":"rules","object_label":"全局规则"}
{"version":2,"operation_id":"r2","phase":"finish","occurred_at":"2026-09-12T11:00:01+08:00","outcome":"succeeded","changed":false,"module":"rules"}
{"version":2,"operation_id":"bad","phase":"start","occurred_at":"2026-09-12T12:00:00+08:00","actor":"management_api","action":"update","object_type":"rules","object_ref":"rules"}
`
	if err := os.WriteFile(filepath.Join(dir, "2026-09-12.jsonl"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	got := auditQueryRequest(url.Values{"date": {"2026-09-12"}})
	if got.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", got.StatusCode, got.Body)
	}
	var page auditPage
	decodeBody(t, got, &page)
	if page.Total != 3 || len(page.Warnings) != 1 {
		t.Fatalf("page=%+v warnings=%v", page, page.Warnings)
	}
	// v2-required module rejected the module-less v2 start as invalid.
	if page.Warnings[0] != "line 7: invalid_event" {
		t.Fatalf("warnings=%v", page.Warnings)
	}
	if !reflect.DeepEqual(page.ModuleCounts, map[string]int{"rules": 2, "key_binding": 1}) {
		t.Fatalf("module_counts=%v", page.ModuleCounts)
	}
	byID := map[string]auditItem{}
	for _, item := range page.Items {
		byID[item.OperationID] = item
	}
	if item := byID["v1"]; item.Module != "" || item.ObjectLabel != "" || item.Labels != nil {
		t.Fatalf("v1 item gained v2 fields: %+v", item)
	}
	if item := byID["k1"]; item.Module != "key_binding" || item.ObjectLabel != "prod-deepseek" || item.Labels["new"] != "火山方舟" {
		t.Fatalf("v2 item=%+v", item)
	}
	if item := byID["r2"]; item.Module != "rules" || item.ObjectLabel != "全局规则" {
		t.Fatalf("rules item=%+v", item)
	}
	// Filter by module: v1 history maps rules through its object type.
	filtered := auditQueryRequest(url.Values{"date": {"2026-09-12"}, "module": {"rules"}})
	if filtered.StatusCode != 200 {
		t.Fatalf("filtered status=%d", filtered.StatusCode)
	}
	var modulePage auditPage
	decodeBody(t, filtered, &modulePage)
	if modulePage.Module != "rules" || modulePage.Total != 2 {
		t.Fatalf("filtered page=%+v", modulePage)
	}
	for _, item := range modulePage.Items {
		if auditItemModule(item) != "rules" {
			t.Fatalf("foreign module item=%+v", item)
		}
	}
	if !reflect.DeepEqual(modulePage.ModuleCounts, page.ModuleCounts) {
		t.Fatalf("counts changed under filter: %v", modulePage.ModuleCounts)
	}
	for _, module := range []string{"bad", "", "Rules"} {
		resp := auditQueryRequest(url.Values{"module": {module}})
		if resp.StatusCode != 400 || !strings.Contains(string(resp.Body), "invalid_audit_module") {
			t.Fatalf("module %q: %d %s", module, resp.StatusCode, resp.Body)
		}
	}
}

func TestAuditQueryFinishRecordWinsLabelsAndObjectLabel(t *testing.T) {
	state := setupManagementTest(t, Config{Enabled: true})
	dir := filepath.Join(filepath.Dir(state), "model-mapper-plus-audit")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// The start label is unknown for creations; the finish record enriches it.
	fixture := `{"version":2,"operation_id":"c1","phase":"start","occurred_at":"2026-09-12T10:00:00+08:00","actor":"management_api","action":"create","module":"key_binding","object_type":"key_binding","object_ref":"key:••••8f2a","labels":{"old-id":"start label"}}
{"version":2,"operation_id":"c1","phase":"finish","occurred_at":"2026-09-12T10:00:01+08:00","outcome":"succeeded","changed":true,"module":"key_binding","object_label":"created-alias","labels":{"old-id":"finish label","extra-id":"added"}}
`
	if err := os.WriteFile(filepath.Join(dir, "2026-09-12.jsonl"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	got := auditQueryRequest(url.Values{"date": {"2026-09-12"}})
	if got.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", got.StatusCode, got.Body)
	}
	var page auditPage
	decodeBody(t, got, &page)
	if page.Total != 1 {
		t.Fatalf("page=%+v", page)
	}
	item := page.Items[0]
	if item.ObjectLabel != "created-alias" {
		t.Fatalf("label=%q", item.ObjectLabel)
	}
	if item.Labels["old-id"] != "finish label" || item.Labels["extra-id"] != "added" || len(item.Labels) != 2 {
		t.Fatalf("labels=%v", item.Labels)
	}
}
