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
			ResetAt: time.Date(2026, 9, 17, 0, 0, 0, 0, NotificationLocation), ResetKnown: true, RemainingDays: 3, RemainingHours: 12}},
		ResetCards: intPtr(2),
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
		"▍Weekly 窗口", "重置 2026-09-17 00:00:00", "还剩 3 天 12 小时",
		"▍重置卡（2 张）", msgAmountNote,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
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
	if strings.Contains(out, "重置卡") {
		t.Fatal("absent reset cards must not render")
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
