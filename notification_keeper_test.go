package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
  {"key":"codex-primary","groupKey":"5h","groupLabel":"5H 窗口","window_usage_tokens":1200000,"window_usage_cost":1.5,"resetAt":"2026-09-13T19:00:00+08:00"},
  {"key":"codex-primary","groupKey":"Weekly","groupLabel":"Weekly 窗口","resetAt":"2026-09-14T19:00:00+08:00"},
  {"key":"codex-primary","groupKey":"daily","window_usage_tokens":9000,"resetAt":"2026-09-13T19:00:00+08:00"}
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
	}}, srv
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
		if r.URL.Path != "/api/v1/quota/cache" || r.Method != http.MethodPost {
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
	})
	windows, cards, plan, err := src.collectWindows(context.Background(), []string{"ai_1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if plan != "Pro 20x" || cards == nil || *cards != 2 {
		t.Fatalf("plan/cards: %s %v", plan, cards)
	}
	// groupKey 归一为 5h/weekly；未知窗口（daily）不渲染。
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
		default:
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
	windows, cards, plan, err := src.collectWindows(context.Background(), matched, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 0 || cards != nil || plan != "" {
		t.Fatalf("empty cache must yield no windows/cards/plan: %+v %v %q", windows, cards, plan)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(cacheQueries) != 1 {
		t.Fatalf("want exactly one cache query, got %d", len(cacheQueries))
	}
	for _, idx := range cacheQueries[0] {
		if idx == "ai_host_only" {
			t.Fatal("unmatched catalog entry must not produce a window query")
		}
	}
}
