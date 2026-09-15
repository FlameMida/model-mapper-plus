// notification_keeper.go adapts Keeper's existing statistics endpoints for
// key-usage notifications: period consumption via the usage analysis
// composition, 5H/Weekly window usage plus plan/reset cards via the quota
// cache. All failures stay closed codes (*keeperError); bodies, URLs and
// credentials never escape to callers.
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
)

// periodStats is one statistics module's payload: the requested period window,
// per-key channel breakdown, and the window/reset-card/plan data collected
// separately by collectWindows. Incomplete marks a window truncated to
// Keeper's queryable coverage; MissingFrom/MissingTo hold the dropped span.
type periodStats struct {
	Start, End             time.Time
	PeriodKey              string
	Incomplete             bool
	MissingFrom, MissingTo string
	Channels               []channelStats
	Windows                []windowStat
	ResetCards             *int
	Plan                   string
}

// channelStats is one api-key row of the analysis composition. Share is the
// key's fraction of the same period's total tokens across all keys; a zero
// denominator keeps ShareKnown=false so the renderer can show "unknown"
// instead of a fabricated 0%.
type channelStats struct {
	Name, Label, Identity string
	Tokens                int64
	CostUSD               float64
	CostAvailable         bool
	Share                 float64
	ShareKnown            bool
}

// windowStat is one 5H/Weekly window of a bound auth_index. WindowUsageAvailable
// distinguishes "Keeper did not report usage" from a real 0-token window, and
// ResetKnown does the same for the reset time; RemainingDays/Hours floor the
// distance from the observation time.
type windowStat struct {
	GroupKey, Label      string
	UsedTokens           int64
	WindowUsageAvailable bool
	ResetAt              time.Time
	ResetKnown           bool
	RemainingDays        int
	RemainingHours       int
}

// keeperStatsSource collects notification statistics from Keeper. The now
// field is the observation clock, injectable for tests.
type keeperStatsSource struct {
	client *keeperClient
	now    func() time.Time
	// cpa is the optional degraded window/plan source backed by the CPA
	// host's own management quota endpoint (unconfigured = nil).
	cpa *cpaQuotaClient
	// refresh-first throttling and polling knobs; zero values fall back to
	// the package defaults, tests inject a movable clock via sleep.
	refreshMu    sync.Mutex
	lastRefresh  map[string]time.Time
	sleep        func(time.Duration)
	pollInterval time.Duration
	pollBudget   time.Duration
}

// Keeper quota refresh-first policy knobs: every collection round triggers a
// refresh task first (throttled), then polls the cache within a bounded
// budget so a cold identity still gets its windows/plan.
const (
	quotaRefreshThrottle = 60 * time.Second
	quotaPollInterval    = 400 * time.Millisecond
	quotaPollBudget      = 2400 * time.Millisecond
)

// errQuotaCacheEmpty marks a syntactically valid cache response that carries
// no completed entries — the "not refreshed yet" state the poll loop waits
// on. Its closed code stays invalid_response for callers outside the poll.
var errQuotaCacheEmpty = &keeperError{Code: "invalid_response"}

// triggerQuotaRefresh POSTs the refresh endpoint for the given identities,
// throttled to one trigger per quotaRefreshThrottle per index set. Failures
// are silently swallowed: the cache may still hold earlier data and the
// refresh endpoint has no closed codes worth surfacing here.
func (s *keeperStatsSource) triggerQuotaRefresh(ctx context.Context, authIndexes []string) {
	if len(authIndexes) == 0 || s.client == nil {
		return
	}
	key := strings.Join(authIndexes, "\x00")
	now := s.now()
	s.refreshMu.Lock()
	if s.lastRefresh == nil {
		s.lastRefresh = map[string]time.Time{}
	}
	if last, ok := s.lastRefresh[key]; ok && now.Sub(last) < quotaRefreshThrottle {
		s.refreshMu.Unlock()
		return
	}
	s.lastRefresh[key] = now
	s.refreshMu.Unlock()
	body, err := json.Marshal(struct {
		AuthIndexes []string `json:"auth_indexes"`
	}{authIndexes})
	if err != nil {
		return
	}
	resp, err := s.client.authenticatedRequest(ctx, http.MethodPost, "/api/v1/quota/refresh", body)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	_, _ = readKeeperBody(ctx, resp.Body)
}

// quotaCacheHasData reports whether a cache read produced anything the
// renderer could use (windows, reset cards or the plan tier).
func quotaCacheHasData(windows []windowStat, cards *int, plan string) bool {
	return len(windows) > 0 || cards != nil || plan != ""
}

// quotaCacheOnce performs one cache read and parse, returning the raw payload
// for the reset-card fallback path.
func (s *keeperStatsSource) quotaCacheOnce(ctx context.Context, authIndexes []string, now time.Time) ([]windowStat, *int, string, []byte, error) {
	if s.client == nil {
		return nil, nil, "", nil, &keeperError{Code: "configuration_error"}
	}
	body, err := json.Marshal(struct {
		AuthIndexes []string `json:"auth_indexes"`
	}{authIndexes})
	if err != nil {
		return nil, nil, "", nil, &keeperError{Code: "invalid_response"}
	}
	resp, err := s.client.authenticatedRequest(ctx, http.MethodPost, "/api/v1/quota/cache", body)
	if err != nil {
		return nil, nil, "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, "", nil, keeperHTTPError(resp)
	}
	raw, err := readKeeperBody(ctx, resp.Body)
	if err != nil {
		return nil, nil, "", nil, err
	}
	windows, cards, plan, err := parseQuotaCache(raw, now)
	if err != nil {
		return nil, nil, "", raw, err
	}
	return windows, cards, plan, raw, nil
}

// newKeeperStatsSource validates the live plugin configuration the same way
// keeperNameServiceForConfig does: an empty URL means notifications without
// Keeper statistics (caller maps that to its disabled semantics), a stale
// config snapshot or an unusable URL yields the closed configuration_error.
func newKeeperStatsSource(cfg Config) (*keeperStatsSource, string) {
	if strings.TrimSpace(cfg.UsageKeeperURL) == "" {
		return nil, ""
	}
	loadedConfigMu.RLock()
	stale := cfg.UsageKeeperURL != loadedCfg.UsageKeeperURL || cfg.UsageKeeperPasswordEnv != loadedCfg.UsageKeeperPasswordEnv ||
		cfg.CPAManagementURL != loadedCfg.CPAManagementURL || cfg.CPAManagementKeyEnv != loadedCfg.CPAManagementKeyEnv
	loadedConfigMu.RUnlock()
	if stale {
		return nil, "configuration_error"
	}
	client, err := newKeeperClient(cfg.UsageKeeperURL, cfg.UsageKeeperPasswordEnv)
	if err != nil {
		return nil, "configuration_error"
	}
	// The CPA management endpoint is an optional degraded quota source; an
	// unusable URL only disables that leg, never the Keeper statistics.
	var cpa *cpaQuotaClient
	if strings.TrimSpace(cfg.CPAManagementURL) != "" {
		if c, err := newCPAQuotaClient(cfg.CPAManagementURL, cfg.CPAManagementKeyEnv); err == nil {
			cpa = c
		}
	}
	return &keeperStatsSource{client: client, now: time.Now, cpa: cpa}, ""
}

// analysisItem is one api_key_composition row (usage_analysis.go
// analysisCompositionItem): key identity plus the same-period token totals.
type analysisItem struct {
	Key, Label    string
	TotalTokens   int64
	CostUSD       float64
	CostAvailable bool
}

// channelShares sums per-key tokens: the spec denominator is the same-period
// total across all keys, and only a positive total yields known shares.
func channelShares(items []analysisItem) (int64, bool) {
	var total int64
	for _, it := range items {
		total += it.TotalTokens
	}
	return total, total > 0
}

// collectForPeriod builds one period module: resolve the period window
// (periodRange, T03), fetch the analysis composition for Keeper's queryable
// coverage of that window, and convert every row into a channel entry with
// its share of the all-keys denominator.
func (s *keeperStatsSource) collectForPeriod(ctx context.Context, apiKey string, kind ModuleKind, period PeriodKind, now time.Time) (periodStats, error) {
	start, end, ok := periodRange(kind, period, now, NotificationLocation)
	if !ok {
		return periodStats{}, &keeperError{Code: "invalid_response"}
	}
	_, cov, err := s.fetchAnalysis(ctx, start, end, now)
	if err != nil {
		return periodStats{}, err
	}
	stats := periodStats{
		Start: start, End: end, PeriodKey: periodKeyOf(kind, period, now),
		Incomplete: cov.incomplete, MissingFrom: cov.missingFrom, MissingTo: cov.missingTo,
		Channels: []channelStats{},
	}
	rows, _, err := s.collectAuthChannelsPeriod(ctx, apiKey, kind, period, now)
	if err == nil && len(rows) > 0 {
		stats.Channels = rows
		return stats, nil
	}
	items, _, err := s.fetchAnalysis(ctx, start, end, now)
	if err != nil {
		return periodStats{}, err
	}
	total, _ := channelShares(items)
	for _, it := range items {
		cs := channelStats{Name: it.Key, Label: it.Label, Identity: it.Key, Tokens: it.TotalTokens,
			CostUSD: it.CostUSD, CostAvailable: it.CostAvailable, ShareKnown: total > 0}
		if total > 0 {
			cs.Share = float64(it.TotalTokens) / float64(total)
		}
		stats.Channels = append(stats.Channels, cs)
	}
	return stats, nil
}

// periodCoverage reports the gap between the requested period window and what
// Keeper's analysis contract could actually cover (spec: known data stays,
// missing dates are labelled, never counted as zero).
type periodCoverage struct {
	incomplete             bool
	missingFrom, missingTo string
}

// analysisQueryRange maps the requested window onto Keeper's custom
// day-range contract (timeutil.parseCustomUsageDayRange): date-only bounds,
// end no later than today, start no earlier than the recent 365 days.
// Windows that reach further back are truncated and reported as incomplete
// with the dropped date span.
func analysisQueryRange(start, end, now time.Time) (startDate, endDate string, cov periodCoverage) {
	loc := NotificationLocation
	n := now.In(loc)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	ey, em, ed := end.In(loc).Date()
	endDay := time.Date(ey, em, ed, 0, 0, 0, 0, loc)
	if endDay.After(today) {
		endDay = today
	}
	sy, sm, sd := start.In(loc).Date()
	startDay := time.Date(sy, sm, sd, 0, 0, 0, 0, loc)
	earliest := today.AddDate(0, 0, -364)
	if startDay.Before(earliest) {
		cov.incomplete = true
		cov.missingFrom = startDay.Format(time.DateOnly)
		cov.missingTo = earliest.AddDate(0, 0, -1).Format(time.DateOnly)
		startDay = earliest
	}
	if startDay.After(endDay) {
		startDay = endDay
	}
	return startDay.Format(time.DateOnly), endDay.Format(time.DateOnly), cov
}

// fetchAnalysis GETs /api/v1/usage/analysis with the custom day-range filter
// (query names verified against usage_filter.go parseUsageFilterQuery:
// range/unit/start/end) and parses the api_key_composition rows.
func (s *keeperStatsSource) fetchAnalysis(ctx context.Context, start, end, now time.Time) ([]analysisItem, periodCoverage, error) {
	startDate, endDate, cov := analysisQueryRange(start, end, now)
	path := "/api/v1/usage/analysis?range=custom&unit=day&start=" + url.QueryEscape(startDate) + "&end=" + url.QueryEscape(endDate)
	resp, err := s.client.authenticatedRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, cov, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, cov, keeperHTTPError(resp)
	}
	raw, err := readKeeperBody(ctx, resp.Body)
	if err != nil {
		return nil, cov, err
	}
	items, err := parseAnalysisComposition(raw)
	if err != nil {
		return nil, cov, err
	}
	return items, cov, nil
}

func (s *keeperStatsSource) fetchAnalysisPath(ctx context.Context, start, end, now time.Time, apiKeyID string) ([]byte, periodCoverage, error) {
	startDate, endDate, cov := analysisQueryRange(start, end, now)
	path := "/api/v1/usage/analysis?range=custom&unit=day&start=" + url.QueryEscape(startDate) + "&end=" + url.QueryEscape(endDate)
	if apiKeyID != "" {
		path += "&api_key_id=" + url.QueryEscape(apiKeyID)
	}
	resp, err := s.client.authenticatedRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, cov, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, cov, keeperHTTPError(resp)
	}
	raw, err := readKeeperBody(ctx, resp.Body)
	if err != nil {
		return nil, cov, err
	}
	return raw, cov, nil
}

func parseNamedComposition(raw []byte, field string) ([]analysisItem, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, &keeperError{Code: "invalid_response"}
	}
	comp, ok := body[field]
	if !ok || len(comp) == 0 || string(comp) == "null" {
		return []analysisItem{}, nil
	}
	var rows []struct {
		Key           *string  `json:"key"`
		Label         *string  `json:"label"`
		TotalTokens   *int64   `json:"total_tokens"`
		CostUSD       *float64 `json:"cost_usd"`
		CostAvailable *bool    `json:"cost_available"`
	}
	if err := json.Unmarshal(comp, &rows); err != nil {
		return nil, &keeperError{Code: "invalid_response"}
	}
	items := make([]analysisItem, 0, len(rows))
	for _, row := range rows {
		if row.Key == nil || strings.TrimSpace(*row.Key) == "" || row.TotalTokens == nil {
			continue
		}
		item := analysisItem{Key: *row.Key, TotalTokens: *row.TotalTokens}
		if row.Label != nil {
			item.Label = *row.Label
		}
		if row.CostUSD != nil {
			item.CostUSD = *row.CostUSD
		}
		if row.CostAvailable != nil {
			item.CostAvailable = *row.CostAvailable
		}
		items = append(items, item)
	}
	return items, nil
}

func unionAuthItems(files, providers []analysisItem) []analysisItem {
	seen := map[string]analysisItem{}
	order := []string{}
	add := func(it analysisItem) {
		if _, ok := seen[it.Key]; ok {
			return
		}
		seen[it.Key] = it
		order = append(order, it.Key)
	}
	for _, it := range files {
		add(it)
	}
	for _, it := range providers {
		add(it)
	}
	out := make([]analysisItem, 0, len(order))
	for _, k := range order {
		out = append(out, seen[k])
	}
	return out
}

func (s *keeperStatsSource) lookupAPIKeyID(ctx context.Context, apiKey string) (string, error) {
	resp, err := s.client.authenticatedRequest(ctx, http.MethodGet, "/api/v1/usage/api-keys/settings", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", keeperHTTPError(resp)
	}
	raw, err := readKeeperBody(ctx, resp.Body)
	if err != nil {
		return "", err
	}
	var body struct {
		Items []struct {
			ID     string `json:"id"`
			APIKey string `json:"apiKey"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", &keeperError{Code: "invalid_response"}
	}
	want := strings.TrimSpace(apiKey)
	for _, it := range body.Items {
		if strings.TrimSpace(it.APIKey) == want && strings.TrimSpace(it.ID) != "" {
			return strings.TrimSpace(it.ID), nil
		}
	}
	return "", &keeperError{Code: "configuration_error"}
}

func (s *keeperStatsSource) collectAuthChannels(ctx context.Context, apiKey string) ([]channelStats, map[string]int64, error) {
	return s.collectAuthChannelsPeriod(ctx, apiKey, ModuleDaily, PeriodCurrent, s.now())
}

func (s *keeperStatsSource) collectAuthChannelsPeriod(ctx context.Context, apiKey string, kind ModuleKind, period PeriodKind, now time.Time) ([]channelStats, map[string]int64, error) {
	start, end, ok := periodRange(kind, period, now, NotificationLocation)
	if !ok {
		return nil, nil, &keeperError{Code: "invalid_response"}
	}
	wideRaw, _, err := s.fetchAnalysisPath(ctx, start, end, now, "")
	if err != nil {
		return nil, nil, err
	}
	wideFiles, err := parseNamedComposition(wideRaw, "auth_files_composition")
	if err != nil {
		return nil, nil, err
	}
	wideProv, err := parseNamedComposition(wideRaw, "ai_provider_composition")
	if err != nil {
		return nil, nil, err
	}
	wide := unionAuthItems(wideFiles, wideProv)
	totals := map[string]int64{}
	for _, it := range wide {
		totals[it.Key] = it.TotalTokens
	}
	source := wide
	if strings.TrimSpace(apiKey) != "" {
		id, err := s.lookupAPIKeyID(ctx, apiKey)
		if err != nil {
			return nil, nil, err
		}
		filtRaw, _, err := s.fetchAnalysisPath(ctx, start, end, now, id)
		if err != nil {
			return nil, nil, err
		}
		ff, err := parseNamedComposition(filtRaw, "auth_files_composition")
		if err != nil {
			return nil, nil, err
		}
		fp, err := parseNamedComposition(filtRaw, "ai_provider_composition")
		if err != nil {
			return nil, nil, err
		}
		source = unionAuthItems(ff, fp)
	}
	rows := make([]channelStats, 0, len(source))
	for _, it := range source {
		total := totals[it.Key]
		cs := channelStats{Name: it.Key, Label: it.Label, Identity: it.Key, Tokens: it.TotalTokens,
			CostUSD: it.CostUSD, CostAvailable: it.CostAvailable, ShareKnown: total > 0}
		if total > 0 {
			cs.Share = float64(it.TotalTokens) / float64(total)
		}
		rows = append(rows, cs)
	}
	return rows, totals, nil
}

// parseAnalysisComposition strictly validates identity fields (key,
// total_tokens must be present) and copies display fields nil-safely; any
// shape violation is the closed invalid_response code.
func parseAnalysisComposition(raw []byte) ([]analysisItem, error) {
	var body struct {
		Composition json.RawMessage `json:"api_key_composition"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || len(body.Composition) == 0 || string(body.Composition) == "null" {
		return nil, &keeperError{Code: "invalid_response"}
	}
	var rows []struct {
		Key           *string  `json:"key"`
		Label         *string  `json:"label"`
		TotalTokens   *int64   `json:"total_tokens"`
		CostUSD       *float64 `json:"cost_usd"`
		CostAvailable *bool    `json:"cost_available"`
	}
	if err := json.Unmarshal(body.Composition, &rows); err != nil {
		return nil, &keeperError{Code: "invalid_response"}
	}
	items := make([]analysisItem, 0, len(rows))
	for _, row := range rows {
		if row.Key == nil || strings.TrimSpace(*row.Key) == "" || row.TotalTokens == nil {
			return nil, &keeperError{Code: "invalid_response"}
		}
		item := analysisItem{Key: *row.Key, TotalTokens: *row.TotalTokens}
		if row.Label != nil {
			item.Label = *row.Label
		}
		if row.CostUSD != nil {
			item.CostUSD = *row.CostUSD
		}
		if row.CostAvailable != nil {
			item.CostAvailable = *row.CostAvailable
		}
		items = append(items, item)
	}
	return items, nil
}

// collectWindows collects 5H/Weekly window usage, the plan tier and the
// reset-card count for exact auth_index identities (already bridged by
// identifyAuthIndex). Only rows whose normalized groupKey is 5h or weekly are
// rendered; missing usage/reset fields keep their "unknown" flags. The reset
// credits endpoint supplements the cached available count when the cache
// entry does not carry one; wantResetCards=false skips that supplementary
// round trips for callers that only need the plan (stats-only notifications).
//
// Refresh-first (2026-09-15): every round triggers a Keeper refresh task
// before reading the cache (throttled per quotaRefreshThrottle), then polls
// the cache inside a bounded budget. A cache that never yields data degrades
// to an empty outcome instead of an error, so stats-only notifications keep
// delivering and windowed ones skip their missing sections.
func (s *keeperStatsSource) collectWindows(ctx context.Context, authIndexes []string, now time.Time, wantResetCards bool) ([]windowStat, *int, string, error) {
	s.triggerQuotaRefresh(ctx, authIndexes)
	windows, cards, plan, raw, err := s.quotaCacheOnce(ctx, authIndexes, now)
	if err != nil && !errors.Is(err, errQuotaCacheEmpty) {
		return nil, nil, "", err
	}
	if !quotaCacheHasData(windows, cards, plan) {
		interval := s.pollInterval
		if interval <= 0 {
			interval = quotaPollInterval
		}
		budget := s.pollBudget
		if budget <= 0 {
			budget = quotaPollBudget
		}
		sleep := s.sleep
		clockNow := time.Now
		if sleep == nil {
			sleep = time.Sleep
		} else {
			// Injected sleep means a movable test clock: poll timing reads
			// the source clock so sleep can advance it.
			clockNow = s.now
		}
		start := clockNow()
		for !quotaCacheHasData(windows, cards, plan) {
			if clockNow().Sub(start) >= budget || ctx.Err() != nil {
				break
			}
			sleep(interval)
			if ctx.Err() != nil {
				break
			}
			windows, cards, plan, raw, err = s.quotaCacheOnce(ctx, authIndexes, now)
			if err != nil && !errors.Is(err, errQuotaCacheEmpty) {
				return nil, nil, "", err
			}
		}
	}
	if !quotaCacheHasData(windows, cards, plan) {
		// Last leg of the degradation chain: ask the CPA host's own quota
		// endpoint. Swallowed failures keep the empty outcome closed.
		if fw, fp, ok := s.cpaQuotaFallback(ctx, authIndexes, now); ok {
			windows = append(windows, fw...)
			if plan == "" {
				plan = fp
			}
		}
	}
	// The cached count may be absent; the reset-credits endpoint is the
	// documented supplementary source (closed codes preserved).
	if wantResetCards && cards == nil {
		for _, item := range decodedCacheItems(raw) {
			count, err := s.fetchResetCards(ctx, item)
			if err != nil {
				return nil, nil, "", err
			}
			if count != nil {
				cards = count
				break
			}
		}
	}
	return windows, cards, plan, nil
}

type quotaCacheItemRef struct {
	AuthIndex string `json:"auth_index"`
}

// decodedCacheItems re-reads the cache payload for the reset-card fallback.
func decodedCacheItems(raw []byte) []quotaCacheItemRef {
	var payload struct {
		Items []quotaCacheItemRef `json:"items"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	return payload.Items
}

// fetchResetCards GETs /api/v1/quota/reset-credits/:auth_index (quota.go
// route; ProviderResetCreditsOutput{availableCount, credits}). A nil count
// means "not provided", not zero.
func (s *keeperStatsSource) fetchResetCards(ctx context.Context, item quotaCacheItemRef) (*int, error) {
	if item.AuthIndex == "" {
		return nil, nil
	}
	resp, err := s.client.authenticatedRequest(ctx, http.MethodGet, "/api/v1/quota/reset-credits/"+url.PathEscape(item.AuthIndex), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, keeperHTTPError(resp)
	}
	raw, err := readKeeperBody(ctx, resp.Body)
	if err != nil {
		return nil, err
	}
	var payload struct {
		AvailableCount *int `json:"availableCount"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, &keeperError{Code: "invalid_response"}
	}
	return payload.AvailableCount, nil
}

// parseQuotaCache validates the CacheResponse envelope and projects each
// completed entry's QuotaRows into 5h/weekly windowStats (tierName preferred
// over plan; both empty keeps the plan unknown), the first known
// rateLimitResetCreditsAvailableCount as reset cards, and discards unknown
// group keys (spec: non-existent windows are not rendered).
func parseQuotaCache(raw []byte, now time.Time) ([]windowStat, *int, string, error) {
	var payload struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Items) == 0 || string(payload.Items) == "null" {
		return nil, nil, "", errQuotaCacheEmpty
	}
	var items []struct {
		AuthIndex string `json:"auth_index"`
		Quota     *struct {
			Quota []struct {
				GroupKey          *string `json:"groupKey"`
				GroupLabel        *string `json:"groupLabel"`
				WindowUsageTokens *int64  `json:"window_usage_tokens"`
				ResetAt           *string `json:"resetAt"`
			} `json:"quota"`
			Subscription *struct {
				Plan     *string `json:"plan"`
				TierName *string `json:"tierName"`
			} `json:"subscription"`
			RateLimitResetCreditsAvailableCount *int `json:"rateLimitResetCreditsAvailableCount"`
		} `json:"quota"`
	}
	if err := json.Unmarshal(payload.Items, &items); err != nil {
		return nil, nil, "", &keeperError{Code: "invalid_response"}
	}
	windows := []windowStat{}
	var cards *int
	plan := ""
	for _, item := range items {
		if item.Quota == nil {
			continue
		}
		for _, row := range item.Quota.Quota {
			if row.GroupKey == nil {
				continue
			}
			group := strings.ToLower(strings.TrimSpace(*row.GroupKey))
			if group != "5h" && group != "weekly" {
				continue
			}
			w := windowStat{GroupKey: group}
			if row.GroupLabel != nil {
				w.Label = *row.GroupLabel
			}
			if row.WindowUsageTokens != nil {
				w.WindowUsageAvailable = true
				w.UsedTokens = *row.WindowUsageTokens
			}
			if row.ResetAt != nil {
				if resetAt, err := time.Parse(time.RFC3339, *row.ResetAt); err == nil {
					w.ResetKnown = true
					w.ResetAt = resetAt
					remaining := int64(resetAt.Sub(now).Seconds())
					if remaining < 0 {
						remaining = 0
					}
					w.RemainingDays = int(remaining / 86400)
					w.RemainingHours = int((remaining % 86400) / 3600)
				}
			}
			windows = append(windows, w)
		}
		if item.Quota.RateLimitResetCreditsAvailableCount != nil && cards == nil {
			count := *item.Quota.RateLimitResetCreditsAvailableCount
			cards = &count
		}
		if plan == "" && item.Quota.Subscription != nil {
			if item.Quota.Subscription.TierName != nil && *item.Quota.Subscription.TierName != "" {
				plan = *item.Quota.Subscription.TierName
			} else if item.Quota.Subscription.Plan != nil && *item.Quota.Subscription.Plan != "" {
				plan = *item.Quota.Subscription.Plan
			}
		}
	}
	return windows, cards, plan, nil
}

// identifyAuthIndex bridges CPA directory auth_index values to Keeper
// identities by exact auth_index membership in the Keeper identity catalog
// (keeperAuthNamesForConfig). Name, model or host-ID guessing is forbidden
// (spec「认证索引精确关联」); catalog unavailability surfaces its closed code.
func identifyAuthIndex(cfg Config, authIndexes []string) ([]string, error) {
	catalog := keeperAuthNamesForConfig(cfg, false)
	if catalog.Status != "ready" {
		code := catalog.ErrorCode
		if code == "" {
			if catalog.Status == "disabled" {
				code = "configuration_error"
			} else {
				code = "connection_failed"
			}
		}
		return nil, &keeperError{Code: code}
	}
	known := make(map[string]struct{}, len(catalog.Items))
	for _, item := range catalog.Items {
		if item.AuthIndex != "" {
			known[item.AuthIndex] = struct{}{}
		}
	}
	matched := make([]string, 0, len(authIndexes))
	seen := make(map[string]struct{}, len(authIndexes))
	for _, index := range authIndexes {
		index = strings.TrimSpace(index)
		if index == "" {
			continue
		}
		if _, dup := seen[index]; dup {
			continue
		}
		seen[index] = struct{}{}
		if _, ok := known[index]; ok {
			matched = append(matched, index)
		}
	}
	return matched, nil
}

// periodKeyOf builds the merge-dedup key for one statistics module:
// "daily:2026-09-13" / "monthly:2026-09" / "half:2026H2" / "yearly:2026",
// with ":prev" appended for previous-period modules. Different period kinds
// never merge (spec); interval schedules get their own anchor-based key
// upstream.
func periodKeyOf(kind ModuleKind, period PeriodKind, now time.Time) string {
	n := now.In(NotificationLocation)
	var key string
	switch kind {
	case ModuleDaily:
		key = "daily:" + n.Format(time.DateOnly)
	case ModuleMonthly:
		key = "monthly:" + n.Format("2006-01")
	case ModuleHalfYear:
		half := "H1"
		if n.Month() >= time.July {
			half = "H2"
		}
		key = "half:" + strconv.Itoa(n.Year()) + half
	case ModuleYearly:
		key = "yearly:" + strconv.Itoa(n.Year())
	default:
		return ""
	}
	if period == PeriodPrevious {
		key += ":prev"
	}
	return key
}
