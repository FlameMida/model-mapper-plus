package main

import (
	"strings"
	"testing"
	"time"
)

func renderFixture() (Notification, statsData) {
	n := Notification{ID: "n1", Name: "日报用量通知", Enabled: true, Modules: []ModuleConfig{
		{Kind: ModuleDaily, Period: PeriodCurrent}, {Kind: ModuleYearly, Period: PeriodCurrent},
		{Kind: ModuleWeeklyWindow}, {Kind: ModuleResetCards},
	}}
	d := statsData{
		DisplayName: "研发主账号", Plan: "Pro 20x",
		Periods: map[string]periodStats{
			"daily:2026-09-13": {PeriodKey: "daily:2026-09-13", Channels: []channelStats{
				{Name: "sk-1", Label: "Claude", Tokens: 800000, CostUSD: 12.34, CostAvailable: true, Share: 0.4, ShareKnown: true},
				{Name: "sk-2", Label: "Grok", Tokens: 12000, CostUSD: 0.21, CostAvailable: true, ShareKnown: false},
			}},
			"yearly:2026": {PeriodKey: "yearly:2026", Incomplete: true, MissingFrom: "2026-01-01", MissingTo: "2026-01-04",
				Channels: []channelStats{{Name: "sk-1", Label: "Claude", Tokens: 8640000, CostUSD: 132.5, CostAvailable: true, ShareKnown: false}}},
		},
		Windows: []windowStat{{GroupKey: "weekly", Label: "Weekly", UsedTokens: 2400000, WindowUsageAvailable: true,
			UsedPercent: 25, RemainingPercent: 75, PercentKnown: true,
			ResetAt: time.Date(2026, 9, 17, 0, 0, 0, 0, NotificationLocation), ResetKnown: true, RemainingDays: 3, RemainingHours: 12}},
		ResetCards: intPtr(2),
		ResetCardItems: []resetCardStat{
			{ExpiresAt: time.Date(2026, 9, 13, 19, 0, 0, 0, NotificationLocation), RemainingDays: 0, RemainingHours: 2},
			{ExpiresAt: time.Date(2026, 9, 14, 12, 0, 0, 0, NotificationLocation), RemainingDays: 0, RemainingHours: 19},
		},
	}
	return n, d
}

func intPtr(v int) *int { return &v }

func TestPeriodChannelMetricsEachOnOwnLine(t *testing.T) {
	n := Notification{Name: "日报用量通知", Modules: []ModuleConfig{{Kind: ModuleDaily, Period: PeriodCurrent}}}
	d := statsData{DisplayName: "Codex 主号", Plan: "Pro 20x", Periods: map[string]periodStats{
		"daily:2026-09-13": {Channels: []channelStats{
			{Label: "Codex", Tokens: 800000, Share: 0.4, ShareKnown: true, CostUSD: 12.34, CostAvailable: true},
		}},
	}}
	out, _ := renderMessage(n, d, time.Date(2026, 9, 13, 17, 0, 0, 0, NotificationLocation))
	if !strings.Contains(out, "Codex\n用量 800.00K tokens\n") {
		t.Fatalf("channel name and usage must be on their own lines:\n%s", out)
	}
	if !strings.Contains(out, "占比 40.00%\n") || !strings.Contains(out, "折算 ≈ $12.34") {
		t.Fatalf("share and cost must be on their own lines:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "tokens") && (strings.Contains(line, "%") || strings.Contains(line, "$")) {
			t.Fatalf("metrics packed on one line: %q", line)
		}
	}
}

func TestRenderMessageSectionLayout(t *testing.T) {
	n, d := renderFixture()
	out, _ := renderMessage(n, d, time.Date(2026, 9, 13, 17, 0, 0, 0, NotificationLocation))
	for _, want := range []string{
		"日报用量通知", "研发主账号 · Pro 20x",
		"▍日统计 · 本期累计（2026-09-13）",
		"Claude\n用量 800.00K tokens\n占比 40.00%\n折算 ≈ $12.34",
		"Grok\n用量 12.00K tokens\n占比未知",
		"▍Weekly 窗口", "已用 2.40M tokens", "已用 25.00%", "剩余 75.00%",
		"重置 2026-09-17 00:00:00", "还剩 3 天 12 小时",
		"▍重置卡（2 张）",
		"2026-09-13 19:00:00 到期 · 还剩 0 天 2 小时",
		"2026-09-14 12:00:00 到期 · 还剩 0 天 19 小时",
		"统计时间：2026-09-13 17:00:00",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "金额为 Keeper 价格折算，非上游账单") {
		t.Fatalf("amount disclaimer must not appear:\n%s", out)
	}
}

func TestRenderMessageStatsTimeUsesActualNow(t *testing.T) {
	n := Notification{Name: "日报用量通知", Modules: []ModuleConfig{{Kind: ModuleDaily, Period: PeriodCurrent}}}
	d := statsData{DisplayName: "Codex 主号", Plan: "Pro 20x", Periods: map[string]periodStats{
		"daily:2026-09-15": {Channels: []channelStats{
			{Label: "Codex", Tokens: 1, ShareKnown: true, CostAvailable: false},
		}},
	}}
	now := time.Date(2026, 9, 15, 16, 46, 32, 0, NotificationLocation)
	out, _ := renderMessage(n, d, now)
	if !strings.Contains(out, "统计时间：2026-09-15 16:46:32") {
		t.Fatalf("footer must use the actual render time:\n%s", out)
	}
	if strings.Contains(out, "金额为 Keeper 价格折算，非上游账单") {
		t.Fatalf("amount disclaimer must not appear:\n%s", out)
	}
}

func TestFormatNotificationPlanCoversKnownTiers(t *testing.T) {
	cases := []struct {
		provider, plan, tierName, tierID, want string
	}{
		{"codex", "free", "", "", "Free"},
		{"codex", "plus", "", "", "Plus"},
		{"codex", "team", "", "", "Team"},
		{"codex", "pro-5x", "", "", "Pro 5x"},
		{"codex", "prolite", "", "", "Pro 5x"},
		{"codex", "pro", "", "", "Pro 20x"},
		{"codex", "pro-20x", "", "", "Pro 20x"},
		{"codex", "enterprise", "", "", "Enterprise"},
		{"codex", "ChatGPT-Pro-Monthly", "", "", "ChatGPT-Pro-Monthly"},
		{"claude", "free", "", "", "Free"},
		{"claude", "pro", "", "", "Pro"},
		{"claude", "max", "", "", "Max"},
		{"claude", "team", "", "", "Team"},
		{"antigravity", "free", "", "", "Free"},
		{"antigravity", "pro", "", "", "Pro"},
		{"antigravity", "ultra-lite", "", "", "Ultra Lite"},
		{"antigravity", "ultra", "", "", "Ultra"},
		{"antigravity", "unknown", "Future", "future-tier", "Future"},
		{"", "ultra-lite", "", "", "Ultra Lite"},
		{"", "pro-20x", "", "", "Pro 20x"},
		{"", "pro", "", "", "Pro"},
		{"", "", "", "", ""},
	}
	for _, tc := range cases {
		if got := formatNotificationPlan(tc.provider, tc.plan, tc.tierName, tc.tierID); got != tc.want {
			t.Fatalf("formatNotificationPlan(%q,%q,%q,%q)=%q want %q", tc.provider, tc.plan, tc.tierName, tc.tierID, got, tc.want)
		}
	}
}

func TestRenderMessageFormatsCanonicalCodexPlan(t *testing.T) {
	now := time.Date(2026, 9, 13, 17, 0, 0, 0, NotificationLocation)
	cases := []struct {
		provider, plan, want string
	}{
		{"", "pro-20x", "Pro 20x"},
		{"codex", "pro", "Pro 20x"},
		{"claude", "pro", "Pro"},
		{"claude", "max", "Max"},
		{"", "plus", "Plus"},
		{"antigravity", "ultra-lite", "Ultra Lite"},
		{"antigravity", "ultra", "Ultra"},
		{"codex", "enterprise", "Enterprise"},
	}
	for _, tc := range cases {
		n, d := renderFixture()
		d.PlanProvider, d.Plan = tc.provider, tc.plan
		out, _ := renderMessage(n, d, now)
		want := "研发主账号 · " + tc.want
		if !strings.Contains(out, want) {
			t.Fatalf("provider=%q plan=%q missing %q in:\n%s", tc.provider, tc.plan, want, out)
		}
	}
}

func TestRenderMessageUnknownPlanAndMissingWindow(t *testing.T) {
	n, d := renderFixture()
	d.Plan = ""
	d.Windows = nil // Keeper 未返回 Weekly → 不渲染窗口行（spec Scenario）
	d.ResetCards = nil
	out, _ := renderMessage(n, d, time.Now())
	if !strings.Contains(out, msgUnknownPlan) {
		t.Fatal("missing plan placeholder")
	}
	if strings.Contains(out, "Weekly") {
		t.Fatal("absent window must not render")
	}
	if !strings.Contains(out, "▍重置卡（未提供）") {
		t.Fatalf("checked reset-card module must render 未提供, got:\n%s", out)
	}
}

func TestRenderMessageWindowPercentsAndResetCardExpiry(t *testing.T) {
	n := Notification{Name: "用量", Modules: []ModuleConfig{{Kind: ModuleWindow5H}, {Kind: ModuleResetCards}}}
	d := statsData{
		DisplayName: "Codex 主号", Plan: "Pro 20x",
		Windows: []windowStat{{
			GroupKey: "5h", Label: "5H", WindowUsageAvailable: true, UsedTokens: 1200000,
			UsedPercent: 28, RemainingPercent: 72, PercentKnown: true,
		}},
		ResetCards: intPtr(2),
		ResetCardItems: []resetCardStat{
			{ExpiresAt: time.Date(2026, 9, 13, 19, 0, 0, 0, NotificationLocation), RemainingDays: 0, RemainingHours: 2},
		},
	}
	out, _ := renderMessage(n, d, time.Date(2026, 9, 13, 17, 0, 0, 0, NotificationLocation))
	if !strings.Contains(out, "已用 1.20M tokens\n已用 28.00%\n剩余 72.00%\n") {
		t.Fatalf("5h window must show used tokens then used/remaining percent on their own lines:\n%s", out)
	}
	if !strings.Contains(out, "▍重置卡（2 张）\n2026-09-13 19:00:00 到期 · 还剩 0 天 2 小时\n") {
		t.Fatalf("each reset card must occupy its own expiry line:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "tokens") && strings.Contains(line, "%") {
			t.Fatalf("window metrics packed on one line: %q", line)
		}
	}
}

func TestRenderMessageOmitsUnknownWindowPercents(t *testing.T) {
	n := Notification{Name: "用量", Modules: []ModuleConfig{{Kind: ModuleWeeklyWindow}}}
	d := statsData{DisplayName: "Codex 主号", Plan: "Pro 20x", Windows: []windowStat{{
		GroupKey: "weekly", Label: "Weekly", WindowUsageAvailable: true, UsedTokens: 10,
	}}}
	out, _ := renderMessage(n, d, time.Date(2026, 9, 13, 17, 0, 0, 0, NotificationLocation))
	if strings.Contains(out, "%") {
		t.Fatalf("unknown window percents must be omitted, not invented:\n%s", out)
	}
}

func TestRenderMessagePutsPeriodModulesBeforeWindows(t *testing.T) {
	n, d := renderFixture()
	n.Modules = []ModuleConfig{{Kind: ModuleResetCards}, {Kind: ModuleWindow5H}, {Kind: ModuleDaily, Period: PeriodCurrent}}
	d.Windows = []windowStat{{GroupKey: "5h", Label: "5H", WindowUsageAvailable: true, UsedTokens: 10}}
	d.ResetCards = intPtr(1)
	out, _ := renderMessage(n, d, time.Date(2026, 9, 13, 17, 0, 0, 0, NotificationLocation))
	daily := strings.Index(out, "▍日统计")
	five := strings.Index(out, "▍5H 窗口")
	cards := strings.Index(out, "▍重置卡")
	if daily < 0 || five < 0 || cards < 0 {
		t.Fatalf("missing section in:\n%s", out)
	}
	if daily > five || daily > cards {
		t.Fatalf("日统计 must stay above window modules, got daily=%d 5h=%d cards=%d in:\n%s", daily, five, cards, out)
	}
}

func TestRenderIncompleteAnnotated(t *testing.T) {
	n, d := renderFixture()
	out, warn := renderMessage(n, d, time.Now())
	if !strings.Contains(out, "历史数据不完整") || len(warn.Incomplete) == 0 {
		t.Fatalf("incomplete must be annotated: %s %v", out, warn)
	}
}

func TestAbbreviateTokens(t *testing.T) {
	cases := map[int64]string{999: "999", 1000: "1.00K", 800000: "800.00K", 1200000: "1.20M", 2500000000: "2.50B"}
	for in, want := range cases {
		if got := abbreviateTokens(in); got != want {
			t.Fatalf("%d → %s, want %s", in, got, want)
		}
	}
}
