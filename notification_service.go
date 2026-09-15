// notification_service.go owns the notification lifecycle: boot with a
// recover-on-boot pass, due-schedule generation gated by the throttle (an
// in-flight job for the same target suppresses re-generation so backoff
// waits survive), delivery with the revision-guarded stale-job supersession,
// and stop semantics that drop late results instead of writing them into a
// new configuration (spec lifecycle / hot-reconfigure Scenarios).
//
// Lock discipline: no state lock and no bbolt transaction is held across
// Keeper IO or platform sending; s.mu only guards the status snapshot.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// serviceDeps carries the injectable boundaries: clock, Keeper statistics
// source, adapter factory, run-state store and the poll interval. Tests
// replace Now/Source/NewAdapter; production leaves them zero and the start
// path fills the defaults (NewAdapter=buildAdapter).
type serviceDeps struct {
	Now        func() time.Time
	Source     *keeperStatsSource
	NewAdapter func(PlatformIdentity) (platformAdapter, error)
	Store      *notificationStore
	Tick       time.Duration
}

// notificationStatus is the exposed service health snapshot (consumed by the
// notifications management surface).
type notificationStatus struct {
	Running     bool   `json:"running"`
	ErrorCode   string `json:"error_code,omitempty"`
	Revision    uint64 `json:"revision"`
	PendingJobs int    `json:"pending_jobs"`
	NextFire    string `json:"next_fire,omitempty"`
}

type notificationService struct {
	cfg      Config
	state    State
	deps     serviceDeps
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu     sync.Mutex
	status notificationStatus
}

// activeNotification holds the process-wide instance; restart swaps it under
// the lock, shutdown removes it. A non-running placeholder keeps the last
// unavailable error code visible for the status surface.
var activeNotification struct {
	sync.Mutex
	svc *notificationService
}

// _statsCollector is the package-internal stub hook in front of
// collectForPeriod: tests inject it, production leaves it nil so the real
// T05 collection path runs. Only the HTTP boundary is replaceable; the
// collection logic itself is never bypassed in production.
var _statsCollector func(s *keeperStatsSource, ctx context.Context, apiKey string, kind ModuleKind, period PeriodKind, now time.Time) (periodStats, error)

// _testChannels, when non-nil, is the global auth-channel set used by
// scheduleTick in tests (T03 replaces this with collectAuthChannels).
var _testChannels []string

// collectPeriodStats routes one statistics module through the stub hook or
// the real keeper source.
func collectPeriodStats(s *keeperStatsSource, ctx context.Context, apiKey string, kind ModuleKind, period PeriodKind, now time.Time) (periodStats, error) {
	if _statsCollector != nil {
		return _statsCollector(s, ctx, apiKey, kind, period, now)
	}
	return s.collectForPeriod(ctx, apiKey, kind, period, now)
}

// notificationStorePath places the run-state bbolt file beside the state
// file: same lifecycle, different file.
func notificationStorePath() string {
	return strings.TrimSuffix(stateFilePath(), ".json") + "-notifications.db"
}

// startNotificationService validates the dependencies, recovers in-flight
// jobs left by a previous process (or a hot restart), runs one immediate
// schedule+send pass, then starts the two poll loops. Startup failures are
// controlled errors: callers record an unavailable status and model routing
// is unaffected (spec lifecycle Scenario).
func startNotificationService(cfg Config, st State, store *notificationStore, deps serviceDeps) (*notificationService, error) {
	if deps.Store == nil {
		deps.Store = store
	}
	if deps.Store == nil {
		return nil, &keeperError{Code: "store_unavailable"}
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Tick <= 0 {
		deps.Tick = 15 * time.Second
	}
	if deps.NewAdapter == nil {
		deps.NewAdapter = buildAdapter
	}
	if deps.Source == nil {
		source, code := newKeeperStatsSource(cfg)
		if code != "" {
			return nil, &keeperError{Code: code}
		}
		deps.Source = source
	}
	if err := deps.Store.RecoverOnBoot(); err != nil {
		return nil, &keeperError{Code: "store_unavailable"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &notificationService{
		cfg:    cfg,
		state:  st,
		deps:   deps,
		cancel: cancel,
		status: notificationStatus{Running: true, Revision: deps.Store.Revision()},
	}
	// 启动即评估一轮到期（与轮询周期解耦）：重启补发到期任务，也保证
	// tick 间隔远大于测试观察窗口时首笔投递仍即时完成。
	s.scheduleTick(ctx)
	s.sendTick(ctx)
	s.wg.Add(2)
	go s.loop(ctx, s.scheduleTick)
	go s.loop(ctx, s.sendTick)
	activeNotification.Lock()
	activeNotification.svc = s
	activeNotification.Unlock()
	return s, nil
}

// Stop cancels the loops and waits (bounded by ctx), removes the instance
// from the active slot, then closes the store so a restart can take the file
// lock. In-flight sends observe the canceled context and drop their late
// results; a later boot recovers their sending rows to unknown.
func (s *notificationService) Stop(ctx context.Context) {
	if s.cancel != nil {
		s.cancel()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	activeNotification.Lock()
	if activeNotification.svc == s {
		activeNotification.svc = nil
	}
	activeNotification.Unlock()
	s.mu.Lock()
	s.status.Running = false
	s.mu.Unlock()
	if s.deps.Store != nil {
		_ = s.deps.Store.Close()
	}
}

// Status returns the current health snapshot.
func (s *notificationService) Status() notificationStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *notificationService) loop(ctx context.Context, fn func(context.Context)) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.deps.Tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn(ctx)
		}
	}
}

func globalNotificationList(st *State) []Notification {
	if st == nil || st.Notifications == nil {
		return nil
	}
	if len(st.Notifications.Notifications) > 0 {
		return st.Notifications.Notifications
	}
	return []Notification{st.Notifications.GlobalDefault}
}

func (s *notificationService) authChannelIDs() []string {
	if len(_testChannels) > 0 {
		return _testChannels
	}
	return []string{"-"}
}

func clockScope(fp string) string {
	if fp == "" || fp == "global" {
		return "global"
	}
	return fp
}

// scheduleTick evaluates due global channel jobs and dedicated key
// notifications against persisted clocks. It never treats the poll instant
// as the plan anchor.
func (s *notificationService) scheduleTick(ctx context.Context) {
	now := s.deps.Now()
	st := s.state
	if snap, ok := loadedStateSnapshot(); ok {
		st = snap
	}
	if st.Notifications == nil || !st.Notifications.Enabled {
		s.mu.Lock()
		s.status.NextFire = ""
		s.mu.Unlock()
		return
	}
	var nextFire time.Time
	note := func(next time.Time) {
		if next.IsZero() {
			return
		}
		if nextFire.IsZero() || next.Before(nextFire) {
			nextFire = next
		}
	}
	for _, n := range globalNotificationList(&st) {
		if !n.Enabled || n.Schedule == nil {
			continue
		}
		for _, ch := range s.authChannelIDs() {
			last, _ := s.deps.Store.Clock(n.ID, "global", ch)
			due, next := dueAt(*n.Schedule, last, now, NotificationLocation)
			if due {
				if err := s.enqueueForPeriod(ctx, nil, n, now, ch); err != nil {
					s.setStatusError(err.Error())
				}
				_, follow := dueAt(*n.Schedule, now, now, NotificationLocation)
				note(follow)
			} else {
				note(next)
			}
		}
	}
	for i := range st.KeyBindings {
		binding := &st.KeyBindings[i]
		if !binding.Enabled || len(binding.Notifications) == 0 {
			continue
		}
		fp := keyFingerprint(binding.Key)
		for _, n := range binding.Notifications {
			if !n.Enabled {
				continue
			}
			sched := effectiveSchedule(&st, n)
			if sched == nil {
				continue
			}
			last, _ := s.deps.Store.Clock(n.ID, fp, "-")
			due, next := dueAt(*sched, last, now, NotificationLocation)
			if due {
				if err := s.enqueueForPeriod(ctx, binding, n, now, ""); err != nil {
					s.setStatusError(err.Error())
				}
				_, follow := dueAt(*sched, now, now, NotificationLocation)
				note(follow)
			} else {
				note(next)
			}
		}
	}
	s.mu.Lock()
	s.status.NextFire = formatNextFire(nextFire)
	s.mu.Unlock()
}

// enqueueForPeriod collects the statistics once, renders once, then upserts
// one pending job per enabled platform of the notification. Failures keep
// any existing archive untouched and surface their closed code; they never
// degrade to a zero-value send.
func (s *notificationService) enqueueForPeriod(ctx context.Context, binding *KeyBinding, n Notification, now time.Time, channel string) error {
	fp := "global"
	b := KeyBinding{}
	if binding != nil {
		fp = keyFingerprint(binding.Key)
		b = *binding
	}
	var targets []PlatformIdentity
	for _, p := range n.Platforms {
		if p.Enabled {
			targets = append(targets, p)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	free := false
	for _, p := range targets {
		if !pendingJobExists(s.deps.Store, fp, n.ID, p.Kind, channel) {
			free = true
			break
		}
	}
	if !free {
		return nil
	}
	renderN := n
	renderN.Modules = effectiveModules(&s.state, n)
	data, err := s.collectStats(ctx, &b, renderN, now)
	if err != nil {
		return &keeperError{Code: controlled(err)}
	}
	body, _ := renderMessage(renderN, data, now)
	periodKey := notificationPeriodKey(renderN, now)
	for _, p := range targets {
		if pendingJobExists(s.deps.Store, fp, n.ID, p.Kind, channel) {
			continue
		}
		payload, err := json.Marshal(outboundMessage{Title: n.Name, Body: body, UserIDs: nonEmptyIDs(p.UserIDs)})
		if err != nil {
			return &keeperError{Code: "invalid_response"}
		}
		if _, err := s.deps.Store.UpsertJob(notificationJob{
			ID: newJobID(), KeyFingerprint: fp, NotificationID: n.ID, Platform: p.Kind,
			Channel: channel, PeriodKey: periodKey, State: jobPending, Payload: payload,
			NextAttempt: now, CreatedAt: now, Revision: s.deps.Store.Revision(),
		}); err != nil {
			return &keeperError{Code: "store_write_failed"}
		}
		s.mu.Lock()
		s.status.PendingJobs++
		s.mu.Unlock()
	}
	return nil
}

// collectStats gathers every configured module's data: cumulative statistics
// via the keeper source, window/reset-card material via the auth-index bridge
// and the quota cache.
func (s *notificationService) collectStats(ctx context.Context, binding *KeyBinding, n Notification, now time.Time) (statsData, error) {
	data := statsData{DisplayName: binding.Alias, Periods: map[string]periodStats{}}
	if s.deps.Source == nil {
		return data, &keeperError{Code: "configuration_error"}
	}
	needWindows := false
	for _, m := range n.Modules {
		switch m.Kind {
		case ModuleWindow5H, ModuleWeeklyWindow, ModuleResetCards:
			needWindows = true
		default:
			ps, err := collectPeriodStats(s.deps.Source, ctx, binding.Key, m.Kind, m.Period, now)
			if err != nil {
				return data, err
			}
			data.Periods[ps.PeriodKey] = ps
		}
	}
	if needWindows {
		indexes, err := identifyAuthIndex(s.cfg, []string{binding.Key})
		if err != nil {
			return data, err
		}
		windows, cards, plan, err := s.deps.Source.collectWindows(ctx, indexes, now)
		if err != nil {
			return data, err
		}
		data.Windows = windows
		data.ResetCards = cards
		if data.Plan == "" {
			data.Plan = plan
		}
	}
	return data, nil
}

// sendTick supersedes stale pending jobs left behind by a revision bump,
// claims due jobs and delivers each one. A canceled context stops before any
// new send and drops late results instead of finishing them (spec: a late
// result must not be written under the new configuration).
func (s *notificationService) sendTick(ctx context.Context) {
	rev := s.deps.Store.Revision()
	if superseded, err := supersededStaleJobs(s.deps.Store, rev); err != nil {
		s.setStatusError("store_write_failed")
	} else if superseded > 0 {
		s.mu.Lock()
		s.status.PendingJobs -= superseded
		if s.status.PendingJobs < 0 {
			s.status.PendingJobs = 0
		}
		s.mu.Unlock()
	}
	now := s.deps.Now()
	jobs, err := s.deps.Store.ClaimDueJobs(now, 10)
	if err != nil {
		s.setStatusError("store_write_failed")
		return
	}
	s.mu.Lock()
	s.status.Revision = rev
	s.status.PendingJobs -= len(jobs)
	if s.status.PendingJobs < 0 {
		s.status.PendingJobs = 0
	}
	s.mu.Unlock()
	for _, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		res := s.deliverOne(ctx, j)
		if ctx.Err() != nil {
			return
		}
		if err := s.deps.Store.FinishJob(j.ID, res.Outcome, res.ErrorCode, res.Detail); err != nil {
			s.setStatusError("store_write_failed")
			continue
		}
		if res.Outcome == deliveryAccepted {
			_ = s.deps.Store.SetClock(j.NotificationID, clockScope(j.KeyFingerprint), j.Channel, now)
		}
		if res.Outcome == deliveryFailed && res.RetryAfter > 0 {
			s.requeueForRetry(j, now.Add(res.RetryAfter))
		}
	}
}

// deliverOne rebuilds the adapter for the job's platform and sends the stored
// payload, merging per-part results: any unknown part wins, then any failed
// part (keeping the largest retry hint), otherwise accepted.
func (s *notificationService) deliverOne(ctx context.Context, j notificationJob) deliveryResult {
	var msg outboundMessage
	if err := json.Unmarshal(j.Payload, &msg); err != nil {
		return deliveryResult{Outcome: deliveryUnknown, ErrorCode: "invalid_payload", Detail: "job payload unreadable"}
	}
	identity, ok := identityForJob(s.state, j.NotificationID, j.KeyFingerprint, j.Platform)
	if !ok {
		return deliveryResult{Outcome: deliveryUnknown, ErrorCode: "target_unavailable", Detail: "no enabled platform identity for job"}
	}
	adapter, err := s.deps.NewAdapter(identity)
	if err != nil {
		return deliveryResult{Outcome: deliveryFailed, ErrorCode: controlled(err), Detail: "adapter unavailable"}
	}
	parts, err := adapter.send(ctx, msg)
	if err != nil {
		return transportUnknownResult()
	}
	return mergeDeliveryResults(parts)
}

func identityForJob(st State, nid, fingerprint string, kind PlatformKind) (PlatformIdentity, bool) {
	pick := func(n Notification) (PlatformIdentity, bool) {
		if n.ID != nid {
			return PlatformIdentity{}, false
		}
		for _, p := range n.Platforms {
			if p.Kind == kind && p.Enabled {
				return p, true
			}
		}
		return PlatformIdentity{}, false
	}
	if clockScope(fingerprint) == "global" {
		for _, n := range globalNotificationList(&st) {
			if p, ok := pick(n); ok {
				return p, true
			}
		}
		return PlatformIdentity{}, false
	}
	for i := range st.KeyBindings {
		if keyFingerprint(st.KeyBindings[i].Key) != fingerprint {
			continue
		}
		for _, n := range st.KeyBindings[i].Notifications {
			if p, ok := pick(n); ok {
				return p, true
			}
		}
	}
	return PlatformIdentity{}, false
}

// requeueForRetry re-enqueues a rate-limited job with a backed-off next
// attempt (spec: rate limiting merges into a backoff wait, not a failure
// fabrication).
func (s *notificationService) requeueForRetry(j notificationJob, next time.Time) {
	if _, err := s.deps.Store.UpsertJob(notificationJob{
		ID: newJobID(), KeyFingerprint: j.KeyFingerprint, NotificationID: j.NotificationID,
		Platform: j.Platform, Channel: j.Channel, PeriodKey: j.PeriodKey, State: jobPending,
		Payload: j.Payload, NextAttempt: next, CreatedAt: next,
	}); err != nil {
		s.setStatusError("store_write_failed")
		return
	}
	s.mu.Lock()
	s.status.PendingJobs++
	s.mu.Unlock()
}

func (s *notificationService) setStatusError(code string) {
	s.mu.Lock()
	s.status.ErrorCode = code
	s.mu.Unlock()
}

// supersededStaleJobs marks every pending job stamped with an older config
// revision as superseded: a hot reconfigure must not let pre-reconfigure
// jobs fire under the new configuration (spec revision guard). Returns the
// number of superseded rows.
func supersededStaleJobs(store *notificationStore, rev uint64) (int, error) {
	superseded := 0
	err := store.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketJobs)
		if b == nil {
			return fmt.Errorf("bucket %s missing", bucketJobs)
		}
		// bbolt forbids bucket mutation while iterating: collect keys read-only first.
		type stale struct {
			key []byte
			job notificationJob
		}
		var staleJobs []stale
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var j notificationJob
			if json.Unmarshal(v, &j) != nil || j.State != jobPending || j.Revision >= rev {
				continue
			}
			staleJobs = append(staleJobs, stale{key: append([]byte(nil), k...), job: j})
		}
		now := time.Now().UTC()
		for _, it := range staleJobs {
			it.job.State = jobSuperseded
			it.job.UpdatedAt = now
			updated, err := json.Marshal(it.job)
			if err != nil {
				return err
			}
			if err := b.Put(it.key, updated); err != nil {
				return err
			}
			superseded++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return superseded, nil
}

// pendingJobExists reports whether the merge dimension (fingerprint,
// notification, platform) already has an in-flight pending/sending job —
// the throttle gate upstream of the store merge, keeping regeneration from
// resetting a backoff wait.
func pendingJobExists(store *notificationStore, fingerprint, notificationID string, platform PlatformKind, channel string) bool {
	var exists bool
	_ = store.db.View(func(tx *bolt.Tx) error {
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
			if j.State != jobPending && j.State != jobSending {
				continue
			}
			if j.KeyFingerprint == fingerprint && j.NotificationID == notificationID && j.Platform == platform && j.Channel == channel {
				exists = true
				return nil
			}
		}
		return nil
	})
	return exists
}

// notificationPeriodKey is the merge-dedup period of one generation: fixed
// intervals anchor on the tick instant (every interval is its own period),
// calendar schedules reuse the first statistics module's period key.
func notificationPeriodKey(n Notification, now time.Time) string {
	if n.Schedule != nil && n.Schedule.Kind == ScheduleInterval {
		return "interval:" + now.In(NotificationLocation).Format(time.RFC3339)
	}
	for _, m := range n.Modules {
		if key := periodKeyOf(m.Kind, m.Period, now); key != "" {
			return key
		}
	}
	return "unanchored"
}

// mergeDeliveryResults folds per-part delivery results: any unknown part
// makes the whole delivery unknown (the outcome was never observed), then a
// failed part wins keeping the largest retry hint, otherwise all accepted.
func mergeDeliveryResults(parts []deliveryResult) deliveryResult {
	merged := deliveryResult{Outcome: deliveryAccepted}
	for _, r := range parts {
		if r.Outcome == deliveryUnknown {
			return deliveryResult{Outcome: deliveryUnknown, ErrorCode: r.ErrorCode, Detail: r.Detail}
		}
		if r.Outcome == deliveryFailed {
			if merged.Outcome != deliveryFailed {
				merged = deliveryResult{Outcome: deliveryFailed, ErrorCode: r.ErrorCode, Detail: r.Detail, RetryAfter: r.RetryAfter}
				continue
			}
			if r.RetryAfter > merged.RetryAfter {
				merged.RetryAfter = r.RetryAfter
			}
			if merged.ErrorCode == "" {
				merged.ErrorCode = r.ErrorCode
			}
		}
	}
	return merged
}

// controlled folds any error into its closed code: keeper errors keep their
// code, everything else counts as a connection failure.
func controlled(err error) string {
	var ke *keeperError
	if errors.As(err, &ke) {
		return ke.Code
	}
	return "connection_failed"
}

func newJobID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 16)
}

// restartNotificationServiceWithState stops the running instance and starts
// a fresh one for the given state. Any startup failure keeps model routing
// untouched: the failure is recorded as a controlled unavailable code on a
// placeholder instance instead (spec lifecycle Scenario).
func restartNotificationServiceWithState(st State) {
	activeNotification.Lock()
	old := activeNotification.svc
	activeNotification.svc = nil
	activeNotification.Unlock()
	if old != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		old.Stop(ctx)
		cancel()
	}
	cfg := loadedConfig()
	store, err := openNotificationStore(notificationStorePath())
	if err != nil {
		logger.Warn("notification service unavailable", "err", err)
		setActiveNotificationUnavailable("store_unavailable")
		return
	}
	if _, err := startNotificationService(cfg, st, store, serviceDeps{Store: store}); err != nil {
		logger.Warn("notification service unavailable", "err", err)
		_ = store.Close()
		setActiveNotificationUnavailable(unavailableCode(err))
	}
}

func setActiveNotificationUnavailable(code string) {
	activeNotification.Lock()
	activeNotification.svc = &notificationService{status: notificationStatus{ErrorCode: code}}
	activeNotification.Unlock()
}

func unavailableCode(err error) string {
	code := "store_unavailable"
	var ke *keeperError
	if errors.As(err, &ke) && ke.Code != "" {
		code = ke.Code
	}
	return code
}

// shutdownNotificationService cancels and waits for the running instance on
// the plugin shutdown path (bounded wait, then the store lock is released).
func shutdownNotificationService() {
	activeNotification.Lock()
	old := activeNotification.svc
	activeNotification.svc = nil
	activeNotification.Unlock()
	if old == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	old.Stop(ctx)
}
