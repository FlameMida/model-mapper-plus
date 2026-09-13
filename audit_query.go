package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type auditItem struct {
	OperationID string                 `json:"operation_id"`
	Actor       string                 `json:"actor"`
	Action      string                 `json:"action"`
	Module      string                 `json:"module"`
	ObjectType  string                 `json:"object_type"`
	ObjectRef   string                 `json:"object_ref"`
	ObjectLabel string                 `json:"object_label,omitempty"`
	Labels      map[string]string      `json:"labels,omitempty"`
	StartedAt   time.Time              `json:"started_at"`
	FinishedAt  *time.Time             `json:"finished_at,omitempty"`
	Outcome     string                 `json:"outcome"`
	Changed     *bool                  `json:"changed"`
	Changes     map[string]auditChange `json:"changes"`
	ErrorCode   string                 `json:"error_code,omitempty"`
}

type auditPage struct {
	Date     string `json:"date"`
	Timezone string `json:"timezone"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
	Total    int    `json:"total"`
	Module   string `json:"module,omitempty"`
	// ModuleCounts counts every operation of the day per module, ignoring the
	// module filter so filter chips stay informative on filtered pages.
	ModuleCounts map[string]int `json:"module_counts"`
	Items        []auditItem    `json:"items"`
	Warnings     []string       `json:"warnings"`
}

// auditModules enumerates the module filter values; v1 items map through
// auditItemModule so history keeps filtering under its old object types.
var auditModules = map[string]bool{"rules": true, "key_binding": true, "notifications": true, "keeper_auth_name": true, "other": true}

func auditItemModule(item auditItem) string {
	if item.Module != "" {
		return item.Module
	}
	switch item.ObjectType {
	case "rules":
		return "rules"
	case "key_binding":
		return "key_binding"
	case "keeper_auth_name":
		return "keeper_auth_name"
	default:
		return "other"
	}
}

func managementAuditQuery(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	respond := func(status int, body any) pluginapi.ManagementResponse {
		resp := managementJSON(status, body)
		resp.Headers.Set("Cache-Control", "no-store")
		return resp
	}
	day := req.Query.Get("date")
	if day == "" {
		day = auditNow().In(auditLocation).Format("2006-01-02")
	}
	path, err := auditDayPath(stateFilePath(), day)
	if err != nil {
		return respond(400, map[string]string{"error": "invalid_audit_date"})
	}
	parsePage := func(key string, defaultValue int) (int, bool) {
		values, exists := req.Query[key]
		if !exists {
			return defaultValue, true
		}
		if len(values) != 1 {
			return 0, false
		}
		value, err := strconv.Atoi(values[0])
		return value, err == nil && value >= 1
	}
	page, validPage := parsePage("page", 1)
	size, validSize := parsePage("page_size", 20)
	if !validPage || !validSize || size > 100 {
		return respond(400, map[string]string{"error": "invalid_audit_pagination"})
	}
	module := ""
	if values, exists := req.Query["module"]; exists {
		if len(values) != 1 || !auditModules[values[0]] {
			return respond(400, map[string]string{"error": "invalid_audit_module"})
		}
		module = values[0]
	}
	items, warnings, err := readAuditDay(path)
	if err != nil {
		return respond(http.StatusServiceUnavailable, map[string]string{"error": "audit_unavailable"})
	}
	moduleCounts := map[string]int{}
	for _, item := range items {
		moduleCounts[auditItemModule(item)]++
	}
	if module != "" {
		filtered := items[:0]
		for _, item := range items {
			if auditItemModule(item) == module {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	result := auditPage{Date: day, Timezone: "Asia/Shanghai", Page: page, PageSize: size, Total: len(items),
		Module: module, ModuleCounts: moduleCounts, Items: []auditItem{}, Warnings: warnings}
	// Compare by division before multiplication, so very large valid pages cannot overflow.
	if len(items) > 0 && page-1 <= (len(items)-1)/size {
		start := (page - 1) * size
		end := min(start+size, len(items))
		result.Items = items[start:end]
	}
	return respond(http.StatusOK, result)
}

func readAuditDay(path string) ([]auditItem, []string, error) {
	auditIOMu.Lock()
	defer auditIOMu.Unlock()
	warnings := []string{}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []auditItem{}, warnings, nil
	}
	if err != nil {
		return nil, nil, err
	}
	items := make(map[string]*auditItem)
	lines := bytes.Split(raw, []byte{'\n'})
	for index, line := range lines {
		if len(line) == 0 && index == len(lines)-1 {
			continue
		}
		warn := func(code string) { warnings = append(warnings, fmt.Sprintf("line %d: %s", index+1, code)) }
		if index == len(lines)-1 {
			warn("truncated_line")
			continue
		}
		var event auditEvent
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil {
			warn("invalid_event")
			continue
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF || !validAuditEvent(event) {
			warn("invalid_event")
			continue
		}
		if event.Phase == "start" {
			if _, exists := items[event.OperationID]; exists {
				warn("duplicate_start")
				continue
			}
			outcome := "unknown"
			if auditRunning[event.OperationID] == path {
				outcome = "running"
			}
			items[event.OperationID] = &auditItem{OperationID: event.OperationID, Actor: event.Actor, Action: event.Action,
				Module: event.Module, ObjectType: event.ObjectType, ObjectRef: event.ObjectRef,
				ObjectLabel: event.ObjectLabel, Labels: event.Labels,
				StartedAt: event.OccurredAt, Outcome: outcome, Changes: map[string]auditChange{}}
			continue
		}
		item, exists := items[event.OperationID]
		if !exists {
			warn("orphan_finish")
			continue
		}
		if item.FinishedAt != nil {
			warn("duplicate_finish")
			continue
		}
		if event.OccurredAt.Before(item.StartedAt) {
			warn("invalid_event_order")
			continue
		}
		item.FinishedAt = &event.OccurredAt
		item.Outcome, item.Changed, item.ErrorCode = event.Outcome, event.Changed, event.ErrorCode
		// The finish record carries the enriched snapshot (creations only know
		// their alias after the mutation ran); it wins when present.
		if event.ObjectLabel != "" {
			item.ObjectLabel = event.ObjectLabel
		}
		if len(event.Labels) > 0 {
			merged := make(map[string]string, len(item.Labels)+len(event.Labels))
			for id, label := range item.Labels {
				merged[id] = label
			}
			for id, label := range event.Labels {
				merged[id] = label
			}
			item.Labels = merged
		}
		if event.Changes != nil {
			item.Changes = event.Changes
		}
	}
	result := make([]auditItem, 0, len(items))
	for _, item := range items {
		if auditUnconfirmed[item.OperationID] == path {
			item.Outcome, item.Changed, item.FinishedAt = "unknown", nil, nil
			item.Changes = map[string]auditChange{}
			item.ErrorCode = "audit_write_failed"
		}
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StartedAt.Equal(result[j].StartedAt) {
			return result[i].OperationID > result[j].OperationID
		}
		return result[i].StartedAt.After(result[j].StartedAt)
	})
	return result, warnings, nil
}

func validAuditEvent(event auditEvent) bool {
	if event.OperationID == "" || event.OccurredAt.IsZero() {
		return false
	}
	// Version 2 adds the module field; version-1 history keeps its old rules.
	switch event.Version {
	case 1:
	case 2:
		if event.Module == "" {
			return false
		}
	default:
		return false
	}
	if event.Phase == "start" {
		return event.Actor == "management_api" && event.Action != "" && event.ObjectType != "" && event.ObjectRef != "" && event.Outcome == ""
	}
	if event.Phase != "finish" {
		return false
	}
	switch event.Outcome {
	case "succeeded":
		return event.Changed != nil
	case "failed":
		return event.Changed != nil && !*event.Changed
	case "unknown":
		return event.Changed == nil
	default:
		return false
	}
}
