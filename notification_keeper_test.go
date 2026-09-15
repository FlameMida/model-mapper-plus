package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// analysisBody mirrors the real Keeper contract: GET /api/v1/usage/analysis
// returns api_key_composition[] with key/label/total_tokens/percent/cost_usd/
// cost_available (usage_analysis.go analysisCompositionItem).
const analysisBody = `{"granularity":"day","timezone":"CST",
 "api_key_composition":[
  {"key":"sk-k1","label":"研发主账号","total_tokens":800000,"percent":40,"cost_usd":12.34,"cost_available":true},
  {"key":"sk-k2","label":"另一账号","total_tokens":1200000,"percent":60,"cost_usd":18.9,"cost_available":true}]}`

// quotaCacheBody mirrors the real nested Keeper contract: POST /api/v1/quota/cache
// returns {items:[{auth_index,status,quota?:{id,quota:[QuotaRow],subscription?,
// rateLimitResetCreditsAvailableCount?}}]} (quota/refresh.go CacheResponse,
// quota/service.go CheckResponse, quota/types.go QuotaRow). QuotaRow exposes
// window usage as window_usage_tokens / window_usage_cost (snake_case).
const quotaCacheBody = `{"items":[{"auth_index":"ai_1","status":"completed","quota":{
 "id":"q1","quota":[
  {"key":"rate_limit.primary_window","label":"5h","window_usage_tokens":1200000,"window_usage_cost":1.5,"resetAt":"2026-09-13T19:00:00+08:00"},
  {"key":"rate_limit.secondary_window","label":"Weekly","resetAt":"2026-09-14T19:00:00+08:00"},
  {"key":"rate_limit.daily","label":"daily","window_usage_tokens":9000,"resetAt":"2026-09-13T19:00:00+08:00"}
 ],
 "subscription":{"provider":"codex","plan":"Pro","tierName":"Pro 20x"},
 "rateLimitResetCreditsAvailableCount":2}}]}`

func newStatsSourceStub(t *testing.T, handler http.HandlerFunc) (*keeperStatsSource, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client, err := newKeeperClient(srv.URL, "CPA_KEEPER_LOGIN_PASSWORD")
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return &keeperStatsSource{client: client, now: func() time.Time {
		return time.Date(2026, 9, 13, 17, 0, 0, 0, NotificationLocation)
	}, pollInterval: time.Millisecond, pollBudget: 20 * time.Millisecond}, srv
}

func TestCollectChannelStatsAndShare(t *testing.T) {
	src, _ := newStatsSourceStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/usage/analysis" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Real Keeper contract (usage_filter.go parseUsageFilterQuery →
		// timeutil.ParseUsageQueryRangeWithOptions): custom ranges require
		// range=custom plus start/end; day-unit bounds are date-only.
		q := r.URL.Query()
		if q.Get("range") != "custom" || q.Get("unit") != "day" {
			t.Errorf("time filter query: range=%q unit=%q", q.Get("range"), q.Get("unit"))
		}
		if _, err := time.Parse(time.DateOnly, q.Get("start")); err != nil {
			t.Errorf("start must be date-only: %q (%v)", q.Get("start"), err)
		}
		if _, err := time.Parse(time.DateOnly, q.Get("end")); err != nil {
			t.Errorf("end must be date-only: %q (%v)", q.Get("end"), err)
		}
		w.Write([]byte(analysisBody))
	})
	stats, err := src.collectForPeriod(context.Background(), "sk-k1", ModuleDaily, PeriodCurrent, time.Now())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	// spec 渠道明细 Scenario：800000 → 40.00%，分母 2000000
	if len(stats.Channels) != 2 {
		t.Fatalf("want 2 channels, got %d", len(stats.Channels))
	}
	mine := stats.Channels[0]
	if mine.Tokens != 800000 || !mine.ShareKnown || mine.Share < 0.3999 || mine.Share > 0.4001 {
		t.Fatalf("share mismatch: %+v", mine)
	}
	if mine.Label != "研发主账号" || !mine.CostAvailable || mine.CostUSD != 12.34 {
		t.Fatalf("channel fields mismatch: %+v", mine)
	}
}

const authAnalysisBody = `{"granularity":"day","timezone":"CST",
 "api_key_composition":[{"key":"sk-k1","label":"研发主账号","total_tokens":800000,"percent":40,"cost_usd":12.34,"cost_available":true},
  {"key":"sk-k2","label":"另一账号","total_tokens":1200000,"percent":60,"cost_usd":18.9,"cost_available":true}],
 "auth_files_composition":[
  {"key":"ai_1","label":"Codex","total_tokens":2000000,"percent":100,"cost_usd":31.24,"cost_available":true},
  {"key":"ai_2","label":"Claude","total_tokens":0,"percent":0,"cost_usd":0,"cost_available":true}],
 "ai_provider_composition":[]}`

const keyFilteredAnalysisBody = `{"auth_files_composition":[
  {"key":"ai_1","label":"Codex","total_tokens":800000,"percent":40,"cost_usd":12.34,"cost_available":true}],
 "ai_provider_composition":[]}`

func TestCollectAuthChannelsKeyLevelExcludesOtherKeys(t *testing.T) {
	t.Setenv("CPA_KEEPER_LOGIN_PASSWORD", "stub-secret")
	src, _ := newStatsSourceStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/usage/api-keys/settings":
			w.Write([]byte(`{"items":[{"id":"1","apiKey":"sk-k1","displayKey":"sk-***1"}]}`))
		case "/api/v1/usage/analysis":
			if r.URL.Query().Get("api_key_id") == "1" {
				w.Write([]byte(keyFilteredAnalysisBody))
				return
			}
			w.Write([]byte(authAnalysisBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	rows, _, err := src.collectAuthChannels(context.Background(), "sk-k1")
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range rows {
		if ch.Label == "另一账号" || ch.Identity == "sk-k2" {
			t.Fatalf("other key leaked: %+v", ch)
		}
	}
	if len(rows) != 1 || rows[0].Identity != "ai_1" || rows[0].Label != "Codex" {
		t.Fatalf("want Codex ai_1, got %+v", rows)
	}
}

func TestShareUsesChannelWideDenominator(t *testing.T) {
	t.Setenv("CPA_KEEPER_LOGIN_PASSWORD", "stub-secret")
	src, _ := newStatsSourceStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/usage/api-keys/settings":
			w.Write([]byte(`{"items":[{"id":"1","apiKey":"sk-k1"}]}`))
		case "/api/v1/usage/analysis":
			if r.URL.Query().Get("api_key_id") == "1" {
				w.Write([]byte(keyFilteredAnalysisBody))
				return
			}
			w.Write([]byte(authAnalysisBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	rows, _, err := src.collectAuthChannels(context.Background(), "sk-k1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].ShareKnown || rows[0].Share < 0.399 || rows[0].Share > 0.401 {
		t.Fatalf("share want 0.4, got %+v", rows)
	}
}

func TestAPIKeyMappingFailureClosedCode(t *testing.T) {
	t.Setenv("CPA_KEEPER_LOGIN_PASSWORD", "stub-secret")
	src, _ := newStatsSourceStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/usage/api-keys/settings" {
			w.Write([]byte(`{"items":[{"id":"9","apiKey":"sk-other"}]}`))
			return
		}
		w.Write([]byte(authAnalysisBody))
	})
	_, _, err := src.collectAuthChannels(context.Background(), "sk-k1")
	var ke *keeperError
	if err == nil || !errors.As(err, &ke) || ke.Code != "configuration_error" {
		t.Fatalf("want closed mapping error, got %v", err)
	}
}

func TestShareUnknownWhenDenominatorZero(t *testing.T) {
	src, _ := newStatsSourceStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"api_key_composition":[{"key":"sk-k1","total_tokens":0,"percent":0}]}`))
	})
	stats, err := src.collectForPeriod(context.Background(), "sk-k1", ModuleDaily, PeriodCurrent, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Channels[0].ShareKnown {
		t.Fatal("zero denominator must yield ShareKnown=false, not 0%")
	}
}

func TestKeeperFailureKeepsClosedCode(t *testing.T) {
	t.Setenv("CPA_KEEPER_LOGIN_PASSWORD", "stub-secret")
	src, _ := newStatsSourceStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := src.collectForPeriod(context.Background(), "sk-k1", ModuleDaily, PeriodCurrent, time.Now())
	if err == nil || err.Error() != "authentication_failed" {
		t.Fatalf("want closed code authentication_failed, got %v", err)
	}
}

func TestCollectWindowsParsesQuotaCache(t *testing.T) {
	src, _ := newStatsSourceStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/quota/refresh":
			// refresh-first rounds hit the refresh endpoint before the cache;
			// this test only pins cache parsing, so the trigger is accepted.
			w.Write([]byte(`{"accepted":1,"skipped":0,"limit":1}`))
		case "/api/v1/quota/cache":
			if r.Method != http.MethodPost {
				t.Errorf("got %s %s", r.Method, r.URL.Path)
			}
			var req struct {
				AuthIndexes []string `json:"auth_indexes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if len(req.AuthIndexes) != 1 || req.AuthIndexes[0] != "ai_1" {
				t.Errorf("auth_indexes payload: %v", req.AuthIndexes)
			}
			w.Write([]byte(quotaCacheBody))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	windows, cards, plan, err := src.collectWindows(context.Background(), []string{"ai_1"}, time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	if plan != "Pro 20x" || cards == nil || *cards != 2 {
		t.Fatalf("plan/cards: %s %v", plan, cards)
	}
	// key+label 归一为 5h/weekly；未知窗口（daily）不渲染。
	if len(windows) != 2 {
		t.Fatalf("want 2 windows, got %d: %+v", len(windows), windows)
	}
	fiveHour, weekly := windows[0], windows[1]
	if fiveHour.GroupKey != "5h" || !fiveHour.ResetKnown || !fiveHour.WindowUsageAvailable || fiveHour.UsedTokens != 1200000 {
		t.Fatalf("5h window mismatch: %+v", fiveHour)
	}
	if weekly.GroupKey != "weekly" || weekly.WindowUsageAvailable {
		t.Fatalf("weekly window must keep unknown usage distinct from 0: %+v", weekly)
	}
}

func TestIdentifyAuthIndexExactMatch(t *testing.T) {
	var mu sync.Mutex
	var cacheQueries [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/usage/identities":
			// Real Keeper contract: identities endpoint filtered to codex rows
			// (keeper_auth_names.go parseKeeperAuthNames).
			w.Write([]byte(`{"identities":[
				{"id":"7","identity":"ai_1","alias":"primary","displayName":"Primary","auth_type":1,"type":"codex","provider":"codex","is_deleted":false},
				{"id":"8","identity":"ai_2","alias":"secondary","displayName":"Second","auth_type":1,"type":"codex","provider":"codex","is_deleted":false}]}`))
		case "/api/v1/quota/cache":
			var req struct {
				AuthIndexes []string `json:"auth_indexes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			cacheQueries = append(cacheQueries, req.AuthIndexes)
			mu.Unlock()
			w.Write([]byte(`{"items":[]}`))
		case "/api/v1/quota/refresh":
			// refresh-first rounds trigger the refresh endpoint first.
			w.Write([]byte(`{"accepted":1,"skipped":0,"limit":1}`))
		default:
			if strings.HasPrefix(r.URL.Path, "/api/v1/quota/reset-credits/") {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"unsupported_type"}`))
				return
			}
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	cfg := Config{UsageKeeperURL: srv.URL, UsageKeeperPasswordEnv: "CPA_KEEPER_LOGIN_PASSWORD"}
	setLoadedConfigForTest(cfg)
	t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })

	matched, err := identifyAuthIndex(cfg, []string{"ai_1", "ai_host_only", "ai_2", "ai_1"})
	if err != nil {
		t.Fatal(err)
	}
	// spec「认证索引精确关联」：按 auth_index 精确匹配，宿主-only 条目被剔除，
	// 重复索引去重，顺序保持。
	if len(matched) != 2 || matched[0] != "ai_1" || matched[1] != "ai_2" {
		t.Fatalf("exact-match bridge failed: %v", matched)
	}

	client, err := newKeeperClient(srv.URL, cfg.UsageKeeperPasswordEnv)
	if err != nil {
		t.Fatal(err)
	}
	src := &keeperStatsSource{client: client, now: time.Now}
	windows, cards, plan, err := src.collectWindows(context.Background(), matched, time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 0 || cards != nil || plan != "" {
		t.Fatalf("empty cache must yield no windows/cards/plan: %+v %v %q", windows, cards, plan)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(cacheQueries) == 0 {
		t.Fatalf("want at least one cache query")
	}
	for _, query := range cacheQueries {
		for _, idx := range query {
			if idx == "ai_host_only" {
				t.Fatal("unmatched catalog entry must not produce a window query")
			}
		}
	}
}

// refreshFirstStatsSource builds a source over a stub Keeper with an
// injectable clock advanced by the poll sleep, so refresh-first and polling
// behavior is observable without real waiting.
func refreshFirstStatsSource(t *testing.T, handler http.HandlerFunc) (*keeperStatsSource, *time.Time) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client, err := newKeeperClient(srv.URL, "CPA_KEEPER_LOGIN_PASSWORD")
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	clock := time.Date(2026, 9, 15, 9, 0, 0, 0, NotificationLocation)
	src := &keeperStatsSource{
		client: client,
		now:    func() time.Time { return clock },
		sleep:  func(d time.Duration) { clock = clock.Add(d) },
	}
	return src, &clock
}

// TestCollectWindowsRefreshesBeforeCache pins the refresh-first policy: the
// round triggers a Keeper refresh task first, then polls the cache within a
// bounded budget, so a cold identity still gets windows/plan instead of a
// permanent 未知/未提供.
func TestCollectWindowsRefreshesBeforeCache(t *testing.T) {
	t.Setenv("CPA_KEEPER_LOGIN_PASSWORD", "stub-secret")
	var mu sync.Mutex
	var refreshCalls, cacheReads int
	populated := false
	src, _ := refreshFirstStatsSource(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/quota/refresh":
			mu.Lock()
			refreshCalls++
			mu.Unlock()
			w.Write([]byte(`{"accepted":1,"skipped":0,"limit":1,"tasks":[{"authIndex":"ai_1"}]}`))
		case "/api/v1/quota/cache":
			mu.Lock()
			cacheReads++
			done := populated
			populated = true
			mu.Unlock()
			if !done {
				w.Write([]byte(`{"items":null}`))
				return
			}
			w.Write([]byte(quotaCacheBody))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	windows, cards, plan, err := src.collectWindows(context.Background(), []string{"ai_1"}, time.Now(), false)
	if err != nil {
		t.Fatalf("refresh-first collection must not fail: %v", err)
	}
	if plan != "Pro 20x" || len(windows) != 2 || cards == nil {
		t.Fatalf("polled cache must yield windows/plan/cards, got plan=%q windows=%d cards=%v", plan, len(windows), cards)
	}
	mu.Lock()
	defer mu.Unlock()
	if refreshCalls != 1 {
		t.Fatalf("one collection round must trigger exactly one refresh, got %d", refreshCalls)
	}
	if cacheReads < 2 {
		t.Fatalf("empty cache must be polled again, reads=%d", cacheReads)
	}
}

// TestCollectWindowsRefreshThrottle pins the 60s refresh throttle: repeated
// collections inside the window skip the refresh trigger, an expired window
// triggers again.
func TestCollectWindowsRefreshThrottle(t *testing.T) {
	t.Setenv("CPA_KEEPER_LOGIN_PASSWORD", "stub-secret")
	var mu sync.Mutex
	var refreshCalls int
	src, clockPtr := refreshFirstStatsSource(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/quota/refresh":
			mu.Lock()
			refreshCalls++
			mu.Unlock()
			w.Write([]byte(`{"accepted":1,"skipped":0,"limit":1}`))
		case "/api/v1/quota/cache":
			w.Write([]byte(quotaCacheBody))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	advance := func(d time.Duration) { *clockPtr = clockPtr.Add(d) }
	for i := 0; i < 3; i++ {
		if _, _, _, err := src.collectWindows(context.Background(), []string{"ai_1"}, time.Now(), false); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		advance(30 * time.Second)
	}
	mu.Lock()
	defer mu.Unlock()
	if refreshCalls != 2 {
		t.Fatalf("60s throttle must trigger refresh twice across 3 rounds (~90s), got %d", refreshCalls)
	}
}

// TestCollectWindowsEmptyAfterRefreshStaysClosed pins the degradation end: a
// Keeper that accepts the refresh but never yields a completed cache entry
// returns empty data without an error, so stats-only notifications keep
// delivering and windowed ones simply skip their missing sections.
func TestCollectWindowsEmptyAfterRefreshStaysClosed(t *testing.T) {
	t.Setenv("CPA_KEEPER_LOGIN_PASSWORD", "stub-secret")
	src, _ := refreshFirstStatsSource(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/quota/refresh":
			w.Write([]byte(`{"accepted":1,"skipped":0,"limit":1}`))
		case "/api/v1/quota/cache":
			w.Write([]byte(`{"items":null}`))
		case "/api/v1/usage/identities":
			w.Write([]byte(`{"identities":[]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	windows, cards, plan, err := src.collectWindows(context.Background(), []string{"ai_1"}, time.Now(), false)
	if err != nil {
		t.Fatalf("empty-after-refresh must degrade without an error, got %v", err)
	}
	if len(windows) != 0 || cards != nil || plan != "" {
		t.Fatalf("expected empty outcome, got windows=%d cards=%v plan=%q", len(windows), cards, plan)
	}
}

// TestCollectWindowsFallsBackToCPAQuota pins the last leg of the degradation
// chain: when Keeper accepts refreshes but its cache never completes, the
// collector queries the CPA host quota endpoint for every identity and maps
// subscription/groups into windows and the plan tier. The fallback is
// throttled like the refresh trigger.
func TestCollectWindowsFallsBackToCPAQuota(t *testing.T) {
	t.Setenv("CPA_KEEPER_LOGIN_PASSWORD", "stub-secret")
	var mu sync.Mutex
	var cpaCalls int
	cpaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/quota/fetch" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer stub-secret" {
			t.Errorf("management key must ride Authorization, got %q", got)
		}
		var req struct {
			AuthIndex string `json:"auth_index"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.AuthIndex != "ai_1" {
			t.Errorf("auth_index payload: %q", req.AuthIndex)
		}
		mu.Lock()
		cpaCalls++
		mu.Unlock()
		w.Write([]byte(`{"subscription":{"provider":"codex","plan":"pro","tierName":"Pro 20x"},"groups":[
			{"displayName":"5 hours","buckets":[{"window":"5 hours","resetTime":"2026-09-15T20:00:00Z","remainingFraction":0.4}]},
			{"displayName":"Weekly","buckets":[{"window":"Weekly","resetTime":"2026-09-19T00:00:00Z","remainingFraction":0.8}]}]}`))
	}))
	t.Cleanup(cpaSrv.Close)
	src, clockPtr := refreshFirstStatsSource(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/quota/refresh":
			w.Write([]byte(`{"accepted":1,"skipped":0,"limit":1}`))
		case "/api/v1/quota/cache":
			w.Write([]byte(`{"items":null}`))
		case "/api/v1/usage/identities":
			w.Write([]byte(`{"identities":[]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	cpa, err := newCPAQuotaClient(cpaSrv.URL, "CPA_KEEPER_LOGIN_PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	src.cpa = cpa
	advance := func(d time.Duration) { *clockPtr = clockPtr.Add(d) }
	// Round 1 (cold): Keeper stays empty, the CPA fallback answers.
	// Round 2 (61s later): the 60s throttle has expired, CPA answers again.
	// Round 3 (30s later): throttled — the degraded source is skipped and the
	// outcome degrades to empty instead of hammering the endpoint.
	rounds := []struct {
		advance     time.Duration
		wantPlan    string
		wantWindows int
	}{
		{advance: 0, wantPlan: "Pro 20x", wantWindows: 2},
		{advance: 61 * time.Second, wantPlan: "Pro 20x", wantWindows: 2},
		{advance: 30 * time.Second, wantPlan: "", wantWindows: 0},
	}
	for i, tc := range rounds {
		advance(tc.advance)
		windows, cards, plan, err := src.collectWindows(context.Background(), []string{"ai_1"}, *clockPtr, false)
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if plan != tc.wantPlan {
			t.Fatalf("round %d: plan %q, want %q", i, plan, tc.wantPlan)
		}
		if cards != nil {
			t.Fatalf("CPA fallback carries no reset cards")
		}
		if len(windows) != tc.wantWindows {
			t.Fatalf("round %d: want %d windows, got %d", i, tc.wantWindows, len(windows))
		}
		if tc.wantWindows == 0 {
			continue
		}
		byGroup := map[string]windowStat{}
		for _, w := range windows {
			byGroup[w.GroupKey] = w
		}
		five, fiveOK := byGroup["5h"]
		weekly, weeklyOK := byGroup["weekly"]
		if !fiveOK || !weeklyOK {
			t.Fatalf("want 5h and weekly groups, got %v", byGroup)
		}
		if !five.ResetKnown || five.RemainingDays != 0 || five.RemainingHours < 10 || five.RemainingHours > 19 {
			t.Fatalf("5h reset countdown out of range: %+v", five)
		}
		if !weekly.ResetKnown || weekly.RemainingDays != 3 || weekly.RemainingHours < 15 || weekly.RemainingHours > 23 {
			t.Fatalf("weekly reset countdown out of range: %+v", weekly)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if cpaCalls != 2 {
		t.Fatalf("60s throttle must limit CPA fetches to two across 3 rounds (third inside the window), got %d", cpaCalls)
	}
}

// realCodexQuotaCacheBody is the Keeper cache shape actually emitted for Codex
// (quota/normalize.go): rows carry key+label, not groupKey. The historical
// fixture invented groupKey=5h/Weekly and hid the production miss.
const realCodexQuotaCacheBody = `{"items":[{"auth_index":"ai_1","status":"completed","quota":{
 "id":"q1","quota":[
  {"key":"rate_limit.primary_window","label":"5h","window_usage_tokens":1200000,"window_usage_cost":1.5,"resetAt":"2026-09-13T19:00:00+08:00"},
  {"key":"rate_limit.secondary_window","label":"Weekly","resetAt":"2026-09-14T19:00:00+08:00"}
 ],
 "subscription":{"provider":"codex","plan":"pro-20x"},
 "rateLimitResetCreditsAvailableCount":2}}]}`

const cacheWindowsWithoutSubscription = `{"items":[{"auth_index":"ai_1","status":"completed","quota":{
 "id":"q1","quota":[{"key":"rate_limit.primary_window","label":"5h","window_usage_tokens":1200000}]}}]}`

const identitySubscriptionBody = `{"identities":[{
  "id":"7","identity":"ai_1","alias":"primary","displayName":"Primary",
  "auth_type":1,"type":"codex","provider":"codex","is_deleted":false,
  "subscription":{"provider":"codex","plan":"pro-20x"}}]}`

func TestParseIdentityPlanCoversProviders(t *testing.T) {
	want := map[string]struct{}{"ai_1": {}}
	cases := []struct {
		body, label string
	}{
		{`{"identities":[{"identity":"ai_1","subscription":{"provider":"claude","plan":"max"}}]}`, "Max"},
		{`{"identities":[{"identity":"ai_1","subscription":{"provider":"antigravity","plan":"ultra-lite"}}]}`, "Ultra Lite"},
		{`{"identities":[{"identity":"ai_1","subscription":{"provider":"codex","plan":"plus"}}]}`, "Plus"},
		{`{"identities":[{"identity":"ai_1","subscription":{"provider":"antigravity","plan":"unknown","tierName":"Future"}}]}`, "Future"},
	}
	for _, tc := range cases {
		if got := parseIdentityPlan([]byte(tc.body), want); got != tc.label {
			t.Fatalf("parseIdentityPlan=%q want %q body=%s", got, tc.label, tc.body)
		}
	}
}

func TestCollectWindowsFallsBackToIdentitySubscription(t *testing.T) {
	t.Setenv("CPA_KEEPER_LOGIN_PASSWORD", "stub-secret")
	var sawIdentities bool
	src, _ := newStatsSourceStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/quota/refresh":
			w.Write([]byte(`{"accepted":1,"skipped":0,"limit":1}`))
		case "/api/v1/quota/cache":
			w.Write([]byte(cacheWindowsWithoutSubscription))
		case "/api/v1/usage/identities":
			sawIdentities = true
			w.Write([]byte(identitySubscriptionBody))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	_, _, plan, err := src.collectWindows(context.Background(), []string{"ai_1"}, time.Now(), false)
	if err != nil {
		t.Fatalf("identity subscription fallback must not fail: %v", err)
	}
	if !sawIdentities {
		t.Fatal("quota cache without subscription must read identity subscription")
	}
	if plan != "Pro 20x" {
		t.Fatalf("identity plan pro-20x must display as Pro 20x, got %q", plan)
	}
}

const cacheWindowsWithoutResetCards = `{"items":[{"auth_index":"ai_1","status":"completed","quota":{
 "id":"q1","quota":[{"key":"rate_limit.primary_window","label":"5h","window_usage_tokens":1200000}]}}]}`

const mixedCacheWithoutResetCards = `{"items":[
 {"auth_index":"claude_1","status":"completed","quota":{"id":"q-claude","quota":[{"key":"five_hour","label":"5h"}]}},
 {"auth_index":"ai_1","status":"completed","quota":{"id":"q-codex","quota":[{"key":"rate_limit.primary_window","label":"5h"}]}}
]}`

func TestCollectWindowsFetchesCodexResetCardsWhenCacheOmitsCount(t *testing.T) {
	t.Setenv("CPA_KEEPER_LOGIN_PASSWORD", "stub-secret")
	var sawResetCredits bool
	src, _ := newStatsSourceStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/quota/refresh":
			w.Write([]byte(`{"accepted":1,"skipped":0,"limit":1}`))
		case r.URL.Path == "/api/v1/quota/cache":
			w.Write([]byte(cacheWindowsWithoutResetCards))
		case r.URL.Path == "/api/v1/usage/identities":
			w.Write([]byte(identitySubscriptionBody))
		case r.URL.Path == "/api/v1/quota/reset-credits/ai_1":
			sawResetCredits = true
			w.Write([]byte(`{"authIndex":"ai_1","availableCount":2,"credits":[]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	_, cards, _, err := src.collectWindows(context.Background(), []string{"ai_1"}, time.Now(), true)
	if err != nil {
		t.Fatalf("Codex reset-credits supplement must not fail: %v", err)
	}
	if !sawResetCredits {
		t.Fatal("header-style cache without rateLimitResetCreditsAvailableCount must GET reset-credits")
	}
	if cards == nil || *cards != 2 {
		t.Fatalf("Codex reset cards: got %v want 2", cards)
	}
}

func TestCollectWindowsSkipsNonCodexResetCredits(t *testing.T) {
	t.Setenv("CPA_KEEPER_LOGIN_PASSWORD", "stub-secret")
	src, _ := newStatsSourceStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/quota/refresh":
			w.Write([]byte(`{"accepted":2,"skipped":0,"limit":2}`))
		case r.URL.Path == "/api/v1/quota/cache":
			w.Write([]byte(mixedCacheWithoutResetCards))
		case r.URL.Path == "/api/v1/usage/identities":
			w.Write([]byte(`{"identities":[]}`))
		case r.URL.Path == "/api/v1/quota/reset-credits/claude_1":
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"unsupported_type"}`))
		case r.URL.Path == "/api/v1/quota/reset-credits/ai_1":
			w.Write([]byte(`{"authIndex":"ai_1","availableCount":3,"credits":[]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	_, cards, _, err := src.collectWindows(context.Background(), []string{"claude_1", "ai_1"}, time.Now(), true)
	if err != nil {
		t.Fatalf("Claude unsupported reset-credits must not abort Codex cards: %v", err)
	}
	if cards == nil || *cards != 3 {
		t.Fatalf("Codex reset cards after skipping Claude: got %v want 3", cards)
	}
}

func TestCollectWindowsCountsResetCreditsWhenAvailableCountNull(t *testing.T) {
	t.Setenv("CPA_KEEPER_LOGIN_PASSWORD", "stub-secret")
	src, _ := newStatsSourceStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/quota/refresh":
			w.Write([]byte(`{"accepted":1,"skipped":0,"limit":1}`))
		case r.URL.Path == "/api/v1/quota/cache":
			w.Write([]byte(cacheWindowsWithoutResetCards))
		case r.URL.Path == "/api/v1/usage/identities":
			w.Write([]byte(identitySubscriptionBody))
		case r.URL.Path == "/api/v1/quota/reset-credits/ai_1":
			w.Write([]byte(`{"authIndex":"ai_1","availableCount":null,"credits":[{"id":"c1","status":"available"},{"id":"c2","status":"available"}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	_, cards, _, err := src.collectWindows(context.Background(), []string{"ai_1"}, time.Now(), true)
	if err != nil {
		t.Fatalf("null availableCount with credits must not fail: %v", err)
	}
	if cards == nil || *cards != 2 {
		t.Fatalf("credits[] fallback: got %v want 2", cards)
	}
}

func TestParseQuotaCacheAcceptsRealCodexRows(t *testing.T) {
	now := time.Date(2026, 9, 13, 17, 0, 0, 0, NotificationLocation)
	windows, cards, plan, err := parseQuotaCache([]byte(realCodexQuotaCacheBody), now)
	if err != nil {
		t.Fatalf("real Codex cache must parse: %v", err)
	}
	if plan != "Pro 20x" {
		t.Fatalf("plan: got %q want Pro 20x", plan)
	}
	if cards == nil || *cards != 2 {
		t.Fatalf("reset cards: got %v want 2", cards)
	}
	if len(windows) != 2 {
		t.Fatalf("want 5h+weekly from label/key, got %d: %+v", len(windows), windows)
	}
	if windows[0].GroupKey != "5h" || windows[0].Label != "5H" || !windows[0].WindowUsageAvailable || windows[0].UsedTokens != 1200000 || !windows[0].ResetKnown {
		t.Fatalf("5h window: %+v", windows[0])
	}
	if windows[1].GroupKey != "weekly" || windows[1].WindowUsageAvailable {
		t.Fatalf("weekly window: %+v", windows[1])
	}
}

// TestCPAWindowGroupKey covers the free-form provider window label mapping.
func TestCPAWindowGroupKey(t *testing.T) {
	cases := map[string]string{
		"5 hours": "5h", "5h": "5h", "5-hour": "5h",
		"Weekly": "weekly", "7 days week": "weekly",
		"daily": "", "": "", "monthly": "",
	}
	for input, want := range cases {
		if got, ok := cpaWindowGroupKey(input); got != want || ok != (want != "") {
			t.Fatalf("cpaWindowGroupKey(%q) = %q,%v want %q", input, got, ok, want)
		}
	}
}
