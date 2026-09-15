// cpa_quota_client.go is the degraded window/plan source: when Keeper's
// refresh-first round still yields no data, the collector asks the CPA host's
// own management quota endpoint (POST /v0/management/quota/fetch with
// {"auth_index": ...}) which routes to the credential's quota provider.
// Failures stay closed codes; the base URL and the management key never
// escape to callers or logs.
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type cpaQuotaClient struct {
	baseURL   string
	apiKeyEnv string
	http      *http.Client
}

// newCPAQuotaClient validates the management base URL with the same closed
// configuration_error contract as newKeeperClient.
func newCPAQuotaClient(baseURL, apiKeyEnv string) (*cpaQuotaClient, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, &keeperError{Code: "configuration_error"}
	}
	return &cpaQuotaClient{baseURL: strings.TrimRight(u.String(), "/"), apiKeyEnv: strings.TrimSpace(apiKeyEnv), http: &http.Client{}}, nil
}

func (c *cpaQuotaClient) apiKey() string {
	if c == nil || c.apiKeyEnv == "" {
		return ""
	}
	return os.Getenv(c.apiKeyEnv)
}

// cpaWindowGroupKey maps the free-form provider window label onto the
// notification renderer's 5h/weekly group keys (Keeper lowercases group keys;
// CPA providers emit strings like "5 hours" / "Weekly"). Unknown windows are
// dropped, mirroring the Keeper parse path.
func cpaWindowGroupKey(window string) (string, bool) {
	w := strings.ToLower(strings.TrimSpace(window))
	switch {
	case w == "":
		return "", false
	case strings.Contains(w, "week"):
		return "weekly", true
	case strings.Contains(w, "5h"), strings.Contains(w, "5 hour"), strings.Contains(w, "5-hour"), strings.Contains(w, "five"):
		return "5h", true
	}
	return "", false
}

// cpaWindowStat converts one quota bucket into the renderer's window shape.
// CPA quota carries no used-token figure, so the usage line stays unknown and
// only the reset countdown renders; an unparseable resetTime keeps the whole
// reset block unknown instead of inventing a zero.
func cpaWindowStat(group, label, resetTime string, now time.Time) windowStat {
	w := windowStat{GroupKey: group, Label: label}
	if strings.TrimSpace(resetTime) == "" {
		return w
	}
	resetAt, ok := parseCPAResetTime(resetTime)
	if !ok {
		return w
	}
	w.ResetKnown = true
	w.ResetAt = resetAt
	remaining := int64(resetAt.Sub(now).Seconds())
	if remaining < 0 {
		remaining = 0
	}
	w.RemainingDays = int(remaining / 86400)
	w.RemainingHours = int((remaining % 86400) / 3600)
	return w
}

func parseCPAResetTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if at, err := time.Parse(layout, raw); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

// fetchQuota queries one credential's quota and projects it into the
// collector's shapes: the plan tier (tierName preferred over plan) and the
// 5h/weekly window rows. A missing management key means the degraded source
// is not usable — closed configuration_error.
func (c *cpaQuotaClient) fetchQuota(ctx context.Context, authIndex string, now time.Time) ([]windowStat, string, error) {
	key := c.apiKey()
	if key == "" {
		return nil, "", &keeperError{Code: "configuration_error"}
	}
	body, err := json.Marshal(map[string]string{"auth_index": authIndex})
	if err != nil {
		return nil, "", &keeperError{Code: "invalid_response"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v0/management/quota/fetch", strings.NewReader(string(body)))
	if err != nil {
		return nil, "", &keeperError{Code: "configuration_error"}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", &keeperError{Code: "timeout"}
		}
		return nil, "", &keeperError{Code: "connection_failed"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", keeperHTTPError(resp)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "", &keeperError{Code: "invalid_response"}
	}
	var payload struct {
		Subscription *struct {
			Plan     string `json:"plan"`
			TierName string `json:"tierName"`
			TierID   string `json:"tierId"`
		} `json:"subscription"`
		Groups []struct {
			DisplayName string `json:"displayName"`
			Buckets     []struct {
				Window    string  `json:"window"`
				ResetTime string  `json:"resetTime"`
				Remaining float64 `json:"remainingFraction"`
			} `json:"buckets"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, "", &keeperError{Code: "invalid_response"}
	}
	windows := []windowStat{}
	for _, g := range payload.Groups {
		for _, b := range g.Buckets {
			group, ok := cpaWindowGroupKey(b.Window)
			if !ok {
				continue
			}
			label := b.Window
			if g.DisplayName != "" {
				label = g.DisplayName
			}
			windows = append(windows, cpaWindowStat(group, label, b.ResetTime, now))
		}
	}
	plan := ""
	if payload.Subscription != nil {
		plan = strings.TrimSpace(payload.Subscription.TierName)
		if plan == "" {
			plan = strings.TrimSpace(payload.Subscription.Plan)
		}
	}
	return windows, plan, nil
}

// cpaQuotaFallback collects windows/plan for every identity via the CPA
// management endpoint, throttled like the Keeper refresh trigger. Any failure
// is swallowed: the degraded source may be unconfigured, unauthorized or
// simply out of coverage, and the caller degrades to empty data either way.
func (s *keeperStatsSource) cpaQuotaFallback(ctx context.Context, authIndexes []string, now time.Time) ([]windowStat, string, bool) {
	if s.cpa == nil || len(authIndexes) == 0 {
		return nil, "", false
	}
	key := strings.Join(authIndexes, "\x00")
	s.refreshMu.Lock()
	if s.lastRefresh == nil {
		s.lastRefresh = map[string]time.Time{}
	}
	if last, ok := s.lastRefresh["cpa\x00"+key]; ok && now.Sub(last) < quotaRefreshThrottle {
		s.refreshMu.Unlock()
		return nil, "", false
	}
	s.lastRefresh["cpa\x00"+key] = now
	s.refreshMu.Unlock()

	windows := []windowStat{}
	plan := ""
	okAny := false
	for _, index := range authIndexes {
		w, p, err := s.cpa.fetchQuota(ctx, index, now)
		if err != nil {
			continue
		}
		if len(w) > 0 || p != "" {
			okAny = true
		}
		windows = append(windows, w...)
		if plan == "" {
			plan = p
		}
	}
	return windows, plan, okAny
}
