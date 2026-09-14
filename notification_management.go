// notification_management.go implements the /notifications management
// surface: settings read/replace with plaintext webhook/secret echo (spec
// 2026-09-13), service status, preview over the saved configuration without
// sending, immediate test-send under the management audit, and delivery
// listing plus retry from the original payload.
//
// Store discipline: the run-state bbolt file only tolerates one live handle,
// so handlers reuse the running service's store; when no service is running
// a short-lived store is opened per operation and closed before returning.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const notificationHandleBase = managementHandleBase + "/notifications"

// notificationDeliveriesLimitBounds bound the deliveries page: the default
// keeps casual reads cheap and the cap protects the host from unbounded
// scans; explicit limits beyond the cap clamp to it.
const (
	notificationDeliveriesDefaultLimit = 100
	notificationDeliveriesMaxLimit     = 500
)

var errDeliveryNotFound = errors.New("delivery not found")

// notificationStoreHookMu guards the test-injected store double; production
// never sets it, so the hook only ever sees nil outside tests.
var (
	testNotificationStore   *notificationStore
	notificationStoreHookMu sync.Mutex
)

// setNotificationStoreForTest injects (or clears, with nil) the store double
// used by the notifications management surface.
func setNotificationStoreForTest(s *notificationStore) {
	notificationStoreHookMu.Lock()
	testNotificationStore = s
	notificationStoreHookMu.Unlock()
}

func injectedNotificationStore() *notificationStore {
	notificationStoreHookMu.Lock()
	defer notificationStoreHookMu.Unlock()
	return testNotificationStore
}

// activeNotificationStore returns the store owned by the running service, if
// any; the placeholder kept for an unavailable service carries none.
func activeNotificationStore() *notificationStore {
	activeNotification.Lock()
	svc := activeNotification.svc
	activeNotification.Unlock()
	if svc == nil {
		return nil
	}
	return svc.deps.Store
}

// withNotificationStore runs fn with the run-state store management should
// use: the injected test double wins, then the running service's own store;
// otherwise a short-lived store is opened for the duration of fn and closed
// before returning (the bbolt file lock must never be held across a service
// restart).
func withNotificationStore(fn func(*notificationStore) error) error {
	if s := injectedNotificationStore(); s != nil {
		return fn(s)
	}
	if s := activeNotificationStore(); s != nil {
		return fn(s)
	}
	store, err := openNotificationStore(notificationStorePath())
	if err != nil {
		return &keeperError{Code: "store_unavailable"}
	}
	defer store.Close()
	return fn(store)
}

// handleNotificationManagement routes the /notifications subtree; it runs
// after the main dispatch switch and reports false for unknown paths so the
// caller keeps its 404. A query string riding on the forwarded path (the raw
// management entrance) is split off and merged into req.Query, matching what
// the host supplies as structured fields.
func handleNotificationManagement(req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, bool) {
	path := managementRequestPath(req.Path)
	if idx := strings.IndexByte(req.Path, '?'); idx >= 0 {
		if parsed, err := url.ParseQuery(req.Path[idx+1:]); err == nil {
			if req.Query == nil {
				req.Query = url.Values{}
			}
			for key, values := range parsed {
				req.Query[key] = append(req.Query[key], values...)
			}
		}
	}
	switch {
	case req.Method == http.MethodGet && path == notificationHandleBase+"/settings":
		return managementNotificationSettingsGet(), true
	case req.Method == http.MethodPut && path == notificationHandleBase+"/settings":
		return auditedStateManagement(req, managementNotificationSettingsPut), true
	case req.Method == http.MethodGet && path == notificationHandleBase+"/status":
		return managementNotificationStatus(), true
	case req.Method == http.MethodPost && path == notificationHandleBase+"/preview":
		return managementNotificationPreview(req), true
	case req.Method == http.MethodPost && path == notificationHandleBase+"/test-send":
		return withAuditedManagement(req, func() (pluginapi.ManagementResponse, auditResult) {
			resp := managementNotificationTestSend(req)
			// A test send mutates no configuration; changed stays explicitly
			// false so the finish event passes version validation.
			changed := false
			result := auditResult{Outcome: "succeeded", Changed: &changed, Changes: map[string]auditChange{}}
			if resp.StatusCode >= 400 {
				code := "invalid_request"
				if resp.StatusCode == http.StatusNotFound {
					code = "not_found"
				}
				result = auditResult{Outcome: "failed", Changed: &changed, ErrorCode: code}
			}
			return resp, result
		}), true
	case req.Method == http.MethodGet && path == notificationHandleBase+"/deliveries":
		return managementNotificationDeliveries(req), true
	case req.Method == http.MethodPost && path == notificationHandleBase+"/deliveries/retry":
		return managementNotificationRetry(req), true
	case req.Method == http.MethodPost && path == notificationHandleBase+"/fetch-members":
		// Read-only over request-body credentials: no audit gate and no
		// management mutation mutex — a slow upstream fetch must not block
		// other management writes.
		return managementNotificationFetchMembers(req), true
	}
	return pluginapi.ManagementResponse{}, false
}

// managementNotificationSettingsGet echoes the saved settings with plaintext
// webhook URLs and sign secrets (spec 2026-09-13); a state without
// notifications returns the zero-value default entity so the UI starts from
// a renderable shape.
func managementNotificationSettingsGet() pluginapi.ManagementResponse {
	st, _ := loadedStateSnapshot()
	settings := st.Notifications
	if settings == nil {
		zero := NotificationSettings{GlobalDefault: Notification{
			Modules: []ModuleConfig{}, Platforms: []PlatformIdentity{},
		}}
		settings = &zero
	}
	return managementJSON(http.StatusOK, settings)
}

// managementNotificationSettingsPut replaces the global notification block:
// validate through applyStateUpdate (spec validation anchors), bump the
// run-state revision so pending jobs from the old configuration are
// superseded, then restart the service onto the new state. The response is
// the full state (audited by the wrapper).
func managementNotificationSettingsPut(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	var body NotificationSettings
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return managementError(http.StatusBadRequest, "通知配置格式无效: "+err.Error())
	}
	err := applyStateUpdate(func(st *State) error {
		st.Notifications = &body
		return validateNotificationSettings(&body)
	})
	if err != nil {
		return managementStateError(err)
	}
	if err := withNotificationStore(func(s *notificationStore) error {
		_, err := s.BumpRevision()
		return err
	}); err != nil {
		// The save itself succeeded and is the user-visible fact; the revision
		// guard degrades until the next successful save and the log records why.
		logger.Warn("notification revision bump failed", "err", err)
	}
	st, _ := loadedStateSnapshot()
	restartNotificationServiceWithState(st)
	return managementGetState()
}

// notificationStatusView extends the service health snapshot with the fields
// the notifications surface needs: the configured global name and the closed
// code explaining why no service is running (store_error).
type notificationStatusView struct {
	notificationStatus
	GlobalName string `json:"global_name"`
	StoreError string `json:"store_error,omitempty"`
}

func managementNotificationStatus() pluginapi.ManagementResponse {
	view := notificationStatusView{GlobalName: notificationGlobalName()}
	activeNotification.Lock()
	svc := activeNotification.svc
	activeNotification.Unlock()
	if svc == nil {
		view.StoreError = "unavailable"
		return managementJSON(http.StatusOK, view)
	}
	view.notificationStatus = svc.Status()
	if !view.Running {
		if view.ErrorCode == "" {
			view.ErrorCode = "unavailable"
		}
		view.StoreError = view.ErrorCode
	}
	return managementJSON(http.StatusOK, view)
}

func notificationGlobalName() string {
	st, _ := loadedStateSnapshot()
	if st.Notifications == nil {
		return ""
	}
	return st.Notifications.GlobalDefault.Name
}

// resolveNotificationEntity picks the notification entity a preview or
// test-send operates on: an empty key means the global default; a key selects
// the binding's own list first (an explicit notification_id picks one entry,
// otherwise the first) and falls back to the effective global entity.
func resolveNotificationEntity(st *State, key, notificationID string) (*Notification, *KeyBinding, error) {
	if key == "" {
		if st.Notifications == nil {
			return nil, nil, errors.New("no notification configured")
		}
		n := st.Notifications.GlobalDefault
		return &n, nil, nil
	}
	binding, ok := findKeyBinding(st.KeyBindings, key)
	if !ok {
		return nil, nil, errors.New("key binding not found")
	}
	list := effectiveNotifications(st, &binding)
	if len(list) == 0 {
		return nil, nil, errors.New("no notification configured")
	}
	for i := range list {
		if notificationID != "" && list[i].ID == notificationID {
			return &list[i], &binding, nil
		}
	}
	if notificationID != "" {
		return nil, nil, errors.New("通知不存在：Key 级通知新增/编辑后需先保存 Key 配置，再测试发送")
	}
	return &list[0], &binding, nil
}

// notificationStatsSource exposes the Keeper statistics source the running
// service holds; a nil result means notifications run without Keeper
// statistics (unconfigured or unavailable service).
func notificationStatsSource() *keeperStatsSource {
	activeNotification.Lock()
	svc := activeNotification.svc
	activeNotification.Unlock()
	if svc == nil {
		return nil
	}
	return svc.deps.Source
}

// renderNotificationPreview collects the current statistics for one entity
// and renders the body exactly as the service would. Every collection failure
// becomes a closed warning instead of a fabricated zero value; missing
// sections are skipped by the renderer.
func renderNotificationPreview(binding *KeyBinding, n Notification) (string, []string) {
	if binding == nil {
		binding = &KeyBinding{}
	}
	now := time.Now()
	var data statsData
	var warnings []string
	source := notificationStatsSource()
	if source == nil {
		data = statsData{DisplayName: binding.Alias, Periods: map[string]periodStats{}}
		warnings = append(warnings, "configuration_error")
	} else {
		collector := &notificationService{cfg: loadedConfig(), deps: serviceDeps{Source: source}}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		collected, err := collector.collectStats(ctx, binding, n, now)
		cancel()
		if err != nil {
			warnings = append(warnings, controlled(err))
		}
		data = collected
	}
	text, renderWarn := renderMessage(n, data, now)
	warnings = append(warnings, renderWarn.Incomplete...)
	if warnings == nil {
		warnings = []string{}
	}
	return text, warnings
}

func managementNotificationPreview(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	var body struct {
		Key            string `json:"key"`
		NotificationID string `json:"notification_id"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return managementError(http.StatusBadRequest, "预览请求格式无效: "+err.Error())
	}
	st, _ := loadedStateSnapshot()
	n, binding, err := resolveNotificationEntity(&st, strings.TrimSpace(body.Key), strings.TrimSpace(body.NotificationID))
	if err != nil {
		return managementError(http.StatusNotFound, err.Error())
	}
	text, warnings := renderNotificationPreview(binding, *n)
	return managementJSON(http.StatusOK, map[string]any{"text": text, "warnings": warnings, "bytes": len(text)})
}

// managementNotificationTestSend renders the chosen entity from the saved
// configuration and delivers it immediately: one pending job per enabled
// platform (so the delivery lands in the store's records), one adapter send,
// then the terminal FinishJob carrying the real outcome.
func managementNotificationTestSend(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	var body struct {
		Key            string `json:"key"`
		NotificationID string `json:"notification_id"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return managementError(http.StatusBadRequest, "测试发送请求格式无效: "+err.Error())
	}
	st, _ := loadedStateSnapshot()
	n, binding, err := resolveNotificationEntity(&st, strings.TrimSpace(body.Key), strings.TrimSpace(body.NotificationID))
	if err != nil {
		return managementError(http.StatusNotFound, err.Error())
	}
	var targets []PlatformIdentity
	for _, p := range n.Platforms {
		if p.Enabled {
			targets = append(targets, p)
		}
	}
	if len(targets) == 0 {
		return managementError(http.StatusBadRequest, "没有已启用的平台，无法发送")
	}
	text, _ := renderNotificationPreview(binding, *n)
	fingerprint := ""
	if binding != nil {
		fingerprint = keyFingerprint(binding.Key)
	}
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ids := []string{}
	err = withNotificationStore(func(s *notificationStore) error {
		rev := s.Revision()
		periodKey := notificationPeriodKey(*n, now)
		for _, p := range targets {
			payload, err := json.Marshal(outboundMessage{Title: n.Name, Body: text, UserIDs: nonEmptyIDs(p.UserIDs)})
			if err != nil {
				return err
			}
			jobID := newJobID()
			if _, err := s.UpsertJob(notificationJob{
				ID: jobID, KeyFingerprint: fingerprint, NotificationID: n.ID, Platform: p.Kind,
				PeriodKey: periodKey, State: jobPending, Payload: payload,
				NextAttempt: now, CreatedAt: now, Revision: rev,
			}); err != nil {
				return err
			}
			// A merge dimension reuses the existing pending job; finish that one
			// so the recorded outcome is attached to the row that will be sent.
			actual := pendingJobIDFor(s, fingerprint, n.ID, p.Kind, periodKey)
			if actual == "" {
				actual = jobID
			}
			res := deliverTestMessage(ctx, p, outboundMessage{Title: n.Name, Body: text, UserIDs: nonEmptyIDs(p.UserIDs)})
			if err := s.FinishJob(actual, res.Outcome, res.ErrorCode, res.Detail); err != nil {
				return err
			}
			if id := latestDeliveryIDFor(s, fingerprint, n.ID, p.Kind, actual); id != "" {
				ids = append(ids, id)
			}
		}
		return nil
	})
	if err != nil {
		var ke *keeperError
		if errors.As(err, &ke) {
			return managementError(http.StatusServiceUnavailable, ke.Code)
		}
		return managementError(http.StatusInternalServerError, err.Error())
	}
	return managementJSON(http.StatusOK, map[string]any{"delivery_ids": ids})
}

// deliverTestMessage sends one rendered message to its platform, folding the
// per-part results with the same rules as the service loop.
func deliverTestMessage(ctx context.Context, p PlatformIdentity, msg outboundMessage) deliveryResult {
	adapter, err := buildAdapter(p)
	if err != nil {
		return deliveryResult{Outcome: deliveryFailed, ErrorCode: controlled(err), Detail: "adapter unavailable"}
	}
	parts, err := adapter.send(ctx, msg)
	if err != nil {
		return transportUnknownResult()
	}
	return mergeDeliveryResults(parts)
}

// pendingJobIDFor returns the ID of the pending job on a merge dimension, or
// "" when none exists.
func pendingJobIDFor(s *notificationStore, fingerprint, notificationID string, platform PlatformKind, periodKey string) string {
	var id string
	_ = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketJobs)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var j notificationJob
			if json.Unmarshal(v, &j) != nil {
				continue
			}
			if j.State == jobPending && j.KeyFingerprint == fingerprint && j.NotificationID == notificationID &&
				j.Platform == platform && j.PeriodKey == periodKey {
				id = j.ID
				return nil
			}
		}
		return nil
	})
	return id
}

// latestDeliveryIDFor finds the newest delivery record of one job.
func latestDeliveryIDFor(s *notificationStore, fingerprint, notificationID string, platform PlatformKind, jobID string) string {
	rows, err := s.Deliveries(DeliveryFilter{KeyFingerprint: fingerprint, NotificationID: notificationID, Platform: platform, Limit: 100})
	if err != nil {
		return ""
	}
	for _, r := range rows {
		if r.JobID == jobID {
			return r.ID
		}
	}
	return ""
}

// managementNotificationDeliveries lists delivery records newest-first under
// the requested filter; deliveryRecord fields serialize under their Go names
// so management clients can round-trip them losslessly.
func managementNotificationDeliveries(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	filter := DeliveryFilter{
		KeyFingerprint: strings.TrimSpace(req.Query.Get("key_fingerprint")),
		NotificationID: strings.TrimSpace(req.Query.Get("notification_id")),
		Platform:       PlatformKind(strings.TrimSpace(req.Query.Get("platform"))),
		Outcome:        strings.TrimSpace(req.Query.Get("outcome")),
	}
	limit := notificationDeliveriesDefaultLimit
	if raw := strings.TrimSpace(req.Query.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return managementError(http.StatusBadRequest, "limit 必须为正整数")
		}
		limit = parsed
	}
	if limit > notificationDeliveriesMaxLimit {
		limit = notificationDeliveriesMaxLimit
	}
	filter.Limit = limit
	var rows []deliveryRecord
	err := withNotificationStore(func(s *notificationStore) error {
		got, err := s.Deliveries(filter)
		rows = got
		return err
	})
	if err != nil {
		return managementError(http.StatusServiceUnavailable, "store_unavailable")
	}
	if rows == nil {
		rows = []deliveryRecord{}
	}
	return managementJSON(http.StatusOK, map[string]any{"items": rows})
}

// managementNotificationRetry re-enqueues one delivery's original payload as
// a pending job on the same merge dimension and reports the job that now
// carries it (a dimension with a live pending job reports that one).
func managementNotificationRetry(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return managementError(http.StatusBadRequest, "重试请求格式无效: "+err.Error())
	}
	id := strings.TrimSpace(body.ID)
	if id == "" {
		return managementError(http.StatusBadRequest, "id 为必填")
	}
	var jobID string
	err := withNotificationStore(func(s *notificationStore) error {
		rec, ok := deliveryByID(s, id)
		if !ok {
			return errDeliveryNotFound
		}
		job, ok := jobByID(s, rec.JobID)
		if !ok {
			return errDeliveryNotFound
		}
		now := time.Now().UTC()
		fresh := newJobID()
		if _, err := s.UpsertJob(notificationJob{
			ID: fresh, KeyFingerprint: rec.KeyFingerprint, NotificationID: rec.NotificationID,
			Platform: rec.Platform, PeriodKey: rec.PeriodKey, State: jobPending,
			Payload: job.Payload, NextAttempt: now, CreatedAt: now, Revision: s.Revision(),
		}); err != nil {
			return err
		}
		jobID = fresh
		if actual := pendingJobIDFor(s, rec.KeyFingerprint, rec.NotificationID, rec.Platform, rec.PeriodKey); actual != "" {
			jobID = actual
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errDeliveryNotFound) {
			return managementError(http.StatusNotFound, err.Error())
		}
		return managementError(http.StatusServiceUnavailable, "store_unavailable")
	}
	return managementJSON(http.StatusOK, map[string]string{"job_id": jobID})
}

// deliveryByID scans the delivery bucket for one record ID.
func deliveryByID(s *notificationStore, id string) (deliveryRecord, bool) {
	var rec deliveryRecord
	found := false
	_ = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDeliveries)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var r deliveryRecord
			if json.Unmarshal(v, &r) == nil && r.ID == id {
				rec, found = r, true
				return nil
			}
		}
		return nil
	})
	return rec, found
}

// jobByID scans the job bucket for one job ID.
func jobByID(s *notificationStore, id string) (notificationJob, bool) {
	var job notificationJob
	found := false
	_ = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketJobs)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var j notificationJob
			if json.Unmarshal(v, &j) == nil && j.ID == id {
				job, found = j, true
				return nil
			}
		}
		return nil
	})
	return job, found
}
