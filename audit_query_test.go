package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
