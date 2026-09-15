// notification_service_test.go exercises the notification service seams with
// a controllable clock and stubbed source/adapter boundaries (spec test
// matrix: controllable clock/network boundary); scheduling, throttling and
// revision-check logic itself stays real.
package main

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func serviceTestState() State {
	return State{Notifications: &NotificationSettings{Enabled: true, GlobalDefault: Notification{
		ID: GlobalNotificationID, Name: "用量通知", Enabled: true,
		Modules:   []ModuleConfig{{Kind: ModuleDaily, Period: PeriodCurrent}},
		Schedule:  &NotificationSchedule{Kind: ScheduleInterval, Interval: 60, Time: "09:00:00"},
		Platforms: []PlatformIdentity{{Kind: PlatformFeishu, Enabled: true, Webhook: "https://f", UserIDs: []string{"ou_a"}}},
	}}, KeyBindings: []KeyBinding{{Key: "sk-k1", Alias: "研发主账号", Enabled: true}}}
}

type stubAdapter struct {
	mu      sync.Mutex
	sent    []outboundMessage
	results []deliveryResult
}

func (a *stubAdapter) send(_ context.Context, m outboundMessage) ([]deliveryResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = append(a.sent, m)
	return []deliveryResult{{Outcome: deliveryAccepted}}, nil
}

// count reads the sent length under the adapter lock (the poll loop reads it
// concurrently with send).
func (a *stubAdapter) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.sent)
}

func isolateNotificationServiceGlobals(t *testing.T) {
	t.Helper()
	loadedStateMu.Lock()
	prev := loadedHolder
	loadedHolder = stateHolder{src: ruleSourceFromConfig(defaultConfig())}
	loadedStateMu.Unlock()
	prevCollector := _statsCollector
	prevCh := _testChannels
	t.Cleanup(func() {
		loadedStateMu.Lock()
		loadedHolder = prev
		loadedStateMu.Unlock()
		_statsCollector = prevCollector
		_testChannels = prevCh
	})
}

func TestServiceFiresDueScheduleAndPersistsJob(t *testing.T) {
	isolateNotificationServiceGlobals(t)
	store := openTestStore(t) // T04 测试辅助
	now := time.Date(2026, 9, 13, 9, 0, 30, 0, NotificationLocation)
	clock := now
	var sent stubAdapter
	deps := serviceDeps{Now: func() time.Time { clock = clock.Add(30 * time.Second); return clock },
		NewAdapter: func(PlatformIdentity) (platformAdapter, error) { return &sent, nil },
		Store:      store, Tick: 30 * time.Second,
		Source: stubSourceForState(serviceTestState())}
	svc, err := startNotificationService(Config{UsageKeeperURL: "http://keeper"}, serviceTestState(), store, deps)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && sent.count() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	rows, _ := store.Deliveries(DeliveryFilter{Limit: 10})
	if sent.count() == 0 || len(rows) == 0 || rows[0].Outcome != deliveryAccepted {
		t.Fatalf("due schedule must produce accepted delivery, sent=%d rows=%v", sent.count(), rows)
	}
	if !strings.Contains(sent.sent[0].Body, "用量通知") {
		t.Fatalf("body: %s", sent.sent[0].Body)
	}
	svc.Stop(context.Background())
}

func TestServiceNoDuplicatePendingForSamePeriod(t *testing.T) {
	isolateNotificationServiceGlobals(t)
	// 同周期已有任意非 superseded 任务（含 pending）→ 不再生成（限流合并语义的上游闸门）
	store := openTestStore(t)
	_, err := store.UpsertJob(notificationJob{ID: "seed", KeyFingerprint: keyFingerprint("sk-k1"),
		NotificationID: GlobalNotificationID, Platform: PlatformFeishu, PeriodKey: "interval:2026-09-13T09:00:30+08:00",
		State: jobPending, NextAttempt: time.Now().Add(time.Hour), CreatedAt: time.Now(), Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	generated := collectDueGenerations(t, store, serviceTestState(), time.Now())
	if len(generated) != 0 {
		t.Fatalf("same-period pending must suppress generation, got %v", generated)
	}
}

func TestServiceStaleJobSupersededAfterRevisionBump(t *testing.T) {
	store := openTestStore(t)
	store.BumpRevision() // rev=1：任务在 rev=1 入队
	_, err := store.UpsertJob(notificationJob{ID: "stale", KeyFingerprint: "fp", NotificationID: "n",
		Platform: PlatformFeishu, PeriodKey: "p", State: jobPending, NextAttempt: time.Now(), CreatedAt: time.Now(), Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	store.BumpRevision() // rev=2：配置已变更
	supersededStaleJobs(store, store.Revision())
	due, _ := store.ClaimDueJobs(time.Now().Add(time.Minute), 10)
	if len(due) != 0 {
		t.Fatalf("stale job must be superseded, due=%v", due)
	}
}

// stubSourceForState returns a *keeperStatsSource shaped stub whose statistics
// are served by the package-level _statsCollector hook (one channel at 40%
// share); the HTTP boundary is the only replaced part, T05 logic stays real.
func stubSourceForState(st State) *keeperStatsSource {
	_ = st
	_statsCollector = func(_ *keeperStatsSource, _ context.Context, _ string, kind ModuleKind, period PeriodKind, now time.Time) (periodStats, error) {
		return periodStats{
			PeriodKey: periodKeyOf(kind, period, now),
			Channels: []channelStats{{Name: "sk-k1", Label: "研发主账号",
				Tokens: 400, Share: 0.4, ShareKnown: true}},
		}, nil
	}
	return &keeperStatsSource{now: func() time.Time { return time.Now() }}
}

// collectDueGenerations dry-runs the production generation decision for one
// tick: the same due-check as scheduleTick (lastFire at its zero start value)
// plus the real throttle gate (pendingJobExists), returning the platform
// targets that would be enqueued.
func TestScheduleSecondIntervalTickEnqueues(t *testing.T) {
	isolateNotificationServiceGlobals(t)
	store := openTestStore(t)
	t0 := time.Date(2026, 9, 15, 8, 0, 0, 0, NotificationLocation)
	now := t0
	st := serviceTestState()
	normalizeNotificationSettings(st.Notifications)
	_testChannels = []string{"ai_1"}
	t.Cleanup(func() { _testChannels = nil })
	svc := &notificationService{state: st, deps: serviceDeps{
		Now: func() time.Time { return now }, Store: store, Source: stubSourceForState(st),
	}}
	svc.scheduleTick(context.Background())
	claimed, err := store.ClaimDueJobs(t0.Add(time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range claimed {
		if err := store.FinishJob(j.ID, deliveryAccepted, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetClock(st.Notifications.Notifications[0].ID, "global", "ai_1", t0); err != nil {
		t.Fatal(err)
	}
	now = t0.Add(15 * time.Second)
	svc.scheduleTick(context.Background())
	now = t0.Add(60 * time.Second)
	svc.scheduleTick(context.Background())
	due, _ := store.ClaimDueJobs(now.Add(time.Minute), 20)
	if len(due) == 0 {
		t.Fatal("T+60s must enqueue after a 15s poll")
	}
}

func TestSchedulePollDoesNotSlideInterval(t *testing.T) {
	isolateNotificationServiceGlobals(t)
	store := openTestStore(t)
	t0 := time.Date(2026, 9, 15, 8, 0, 0, 0, NotificationLocation)
	now := t0
	st := serviceTestState()
	st.Notifications.GlobalDefault.Schedule = &NotificationSchedule{Kind: ScheduleInterval, Interval: 86400}
	normalizeNotificationSettings(st.Notifications)
	_testChannels = []string{"ai_1"}
	t.Cleanup(func() { _testChannels = nil })
	svc := &notificationService{state: st, deps: serviceDeps{
		Now: func() time.Time { return now }, Store: store, Source: stubSourceForState(st),
	}}
	svc.scheduleTick(context.Background())
	_ = store.SetClock(st.Notifications.Notifications[0].ID, "global", "ai_1", t0)
	now = t0.Add(15 * time.Second)
	svc.scheduleTick(context.Background())
	want := formatNextFire(t0.Add(24 * time.Hour))
	if svc.Status().NextFire != want {
		t.Fatalf("next_fire=%q want %q", svc.Status().NextFire, want)
	}
}

func TestGlobalChannelLoopIndependentOfKeyBindings(t *testing.T) {
	isolateNotificationServiceGlobals(t)
	store := openTestStore(t)
	st := serviceTestState()
	st.KeyBindings[0].Notifications = []Notification{{
		ID: "key-n", Name: "专属", Enabled: true,
		Modules:   []ModuleConfig{{Kind: ModuleDaily, Period: PeriodCurrent}},
		Schedule:  &NotificationSchedule{Kind: ScheduleInterval, Interval: 60},
		Platforms: []PlatformIdentity{{Kind: PlatformFeishu, Enabled: true, Webhook: "https://k", UserIDs: []string{"ou_k"}}},
	}}
	normalizeNotificationSettings(st.Notifications)
	_testChannels = []string{"ai_1", "ai_2"}
	t.Cleanup(func() { _testChannels = nil })
	now := time.Date(2026, 9, 15, 8, 0, 0, 0, NotificationLocation)
	svc := &notificationService{state: st, deps: serviceDeps{
		Now: func() time.Time { return now }, Store: store, Source: stubSourceForState(st),
	}}
	svc.scheduleTick(context.Background())
	due, _ := store.ClaimDueJobs(now.Add(time.Minute), 20)
	var globalCh int
	for _, j := range due {
		if j.NotificationID == st.Notifications.Notifications[0].ID && (j.Channel == "ai_1" || j.Channel == "ai_2") {
			globalCh++
		}
	}
	if globalCh < 2 {
		t.Fatalf("global must still enqueue both channels, jobs=%+v", due)
	}
}

func TestIdentityUsesNotificationWebhook(t *testing.T) {
	st := State{Notifications: &NotificationSettings{Enabled: true, Notifications: []Notification{{
		ID: "g", Enabled: true, Platforms: []PlatformIdentity{{Kind: PlatformFeishu, Enabled: true, Webhook: "https://g"}},
	}}}, KeyBindings: []KeyBinding{
		{Key: "sk-k1", Enabled: true},
		{Key: "sk-k2", Enabled: true, Notifications: []Notification{{
			ID: "k", Enabled: true, Platforms: []PlatformIdentity{{Kind: PlatformFeishu, Enabled: true, Webhook: "https://k"}},
		}}},
	}}
	got, ok := identityForJob(st, "k", keyFingerprint("sk-k2"), PlatformFeishu)
	if !ok || got.Webhook != "https://k" {
		t.Fatalf("want https://k, got %+v ok=%v", got, ok)
	}
}

func collectDueGenerations(t *testing.T, store *notificationStore, st State, now time.Time) []string {
	t.Helper()
	var generated []string
	for i := range st.KeyBindings {
		binding := &st.KeyBindings[i]
		if !binding.Enabled {
			continue
		}
		for _, n := range effectiveNotifications(&st, binding) {
			if !n.Enabled || n.Schedule == nil {
				continue
			}
			next, ok := nextTrigger(*n.Schedule, time.Time{}, NotificationLocation)
			if !ok || next.After(now) {
				continue
			}
			fp := keyFingerprint(binding.Key)
			for _, p := range n.Platforms {
				if !p.Enabled {
					continue
				}
				if pendingJobExists(store, fp, n.ID, p.Kind, "") {
					continue
				}
				generated = append(generated, string(p.Kind)+"/"+notificationPeriodKey(n, now))
			}
		}
	}
	return generated
}

// statsOnlyNotification builds a notification with cumulative modules only —
// no 5H/weekly window and no reset cards — the shape that previously never
// reached the Keeper quota/cache endpoint and thus never had a plan.
func statsOnlyNotification() Notification {
	return Notification{Name: "验收日报", Enabled: true,
		Modules: []ModuleConfig{{Kind: ModuleDaily, Period: PeriodCurrent}}}
}

// statsCollectorWithIdentity stubs the cumulative statistics with one channel
// row carrying a real auth identity so collectStats has an index to query the
// quota/cache endpoint with.
func statsCollectorWithIdentity() func(*keeperStatsSource, context.Context, string, ModuleKind, PeriodKind, time.Time) (periodStats, error) {
	return func(_ *keeperStatsSource, _ context.Context, _ string, kind ModuleKind, period PeriodKind, now time.Time) (periodStats, error) {
		return periodStats{
			PeriodKey: periodKeyOf(kind, period, now),
			Channels: []channelStats{{Name: "ai_1", Label: "研发主账号", Identity: "ai_1",
				Tokens: 400, Share: 0.4, ShareKnown: true}},
		}, nil
	}
}

// TestCollectStatsFetchesPlanWithoutWindowModules pins the spec guarantee that
// a notification opening line always carries the Keeper plan tier, even when
// no window module is configured (quota/cache used to be gated behind
// needWindows, leaving every stats-only notification at 未知/未提供).
func TestCollectStatsFetchesPlanWithoutWindowModules(t *testing.T) {
	t.Setenv("CPA_KEEPER_LOGIN_PASSWORD", "stub-secret")
	prev := _statsCollector
	_statsCollector = statsCollectorWithIdentity()
	t.Cleanup(func() { _statsCollector = prev })
	src, _ := newStatsSourceStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/quota/refresh":
			// refresh-first rounds trigger the refresh endpoint first.
			w.Write([]byte(`{"accepted":1,"skipped":0,"limit":1}`))
		case "/api/v1/quota/cache":
			w.Write([]byte(quotaCacheBody))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	collector := &notificationService{deps: serviceDeps{Source: src}}
	data, err := collector.collectStats(context.Background(), &KeyBinding{Alias: "研发主账号"},
		statsOnlyNotification(), time.Date(2026, 9, 15, 9, 0, 0, 0, NotificationLocation), "")
	if err != nil {
		t.Fatalf("stats-only collection must not fail: %v", err)
	}
	if data.Plan == "" {
		t.Fatalf("stats-only notification must still carry the Keeper plan, got empty")
	}
}

// TestCollectStatsPlanFailureKeepsStatsOnlyDelivery is the degradation guard:
// when the quota/cache endpoint fails, a stats-only notification must still
// collect (empty plan) instead of failing the whole send the way windowed
// notifications legitimately do.
func TestCollectStatsPlanFailureKeepsStatsOnlyDelivery(t *testing.T) {
	t.Setenv("CPA_KEEPER_LOGIN_PASSWORD", "stub-secret")
	prev := _statsCollector
	_statsCollector = statsCollectorWithIdentity()
	t.Cleanup(func() { _statsCollector = prev })
	src, _ := newStatsSourceStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	collector := &notificationService{deps: serviceDeps{Source: src}}
	data, err := collector.collectStats(context.Background(), &KeyBinding{Alias: "研发主账号"},
		statsOnlyNotification(), time.Date(2026, 9, 15, 9, 0, 0, 0, NotificationLocation), "")
	if err != nil {
		t.Fatalf("plan fetch failure must degrade, not fail stats-only collection: %v", err)
	}
	if data.Plan != "" {
		t.Fatalf("failed plan fetch must keep the plan empty, got %q", data.Plan)
	}
	if len(data.Periods) == 0 {
		t.Fatalf("cumulative statistics must survive the plan fetch failure")
	}
}
