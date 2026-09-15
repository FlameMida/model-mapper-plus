package main

import (
	"fmt"
	"strings"
	"time"
)

const (
	msgUnknownPlan  = "未知/未提供"
	msgUnknownShare = "占比未知"
	msgAmountNote   = "金额为 Keeper 价格折算，非上游账单"
)

// statsData carries everything renderMessage needs: per-period channel stats
// keyed by periodKeyOf output, the Keeper quota windows of the bound identity
// and the reset-card count (nil = Keeper did not provide one).
type statsData struct {
	DisplayName string
	Plan        string
	Periods     map[string]periodStats
	Windows     []windowStat
	ResetCards  *int
}

// renderWarnings lists the annotations renderMessage had to add because the
// source statistics were incomplete, so senders can surface them upstream.
type renderWarnings struct{ Incomplete []string }

// abbreviateTokens renders a token count as 999 / 1.00K / 1.20M / 2.50B.
func abbreviateTokens(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.2fK", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

var moduleHead = map[ModuleKind]string{
	ModuleDaily:    "日统计",
	ModuleMonthly:  "月统计",
	ModuleHalfYear: "半年统计",
	ModuleYearly:   "年统计",
}

// windowModuleGroup maps a window module kind to the Keeper quota groupKey it
// matches (keeper lowercases group keys) plus a label fallback for rendering.
func windowModuleGroup(kind ModuleKind) (group, fallback string, ok bool) {
	switch kind {
	case ModuleWeeklyWindow:
		return "weekly", "Weekly", true
	case ModuleWindow5H:
		return "5h", "5H", true
	}
	return "", "", false
}

// periodRangeLabel folds a same-day range to one date, e.g.（2026-09-13）
// instead of（2026-09-13 ~ 2026-09-13）.
func periodRangeLabel(start, end time.Time) string {
	s := start.In(NotificationLocation).Format(time.DateOnly)
	e := end.In(NotificationLocation).Format(time.DateOnly)
	if s == e {
		return s
	}
	return s + " ~ " + e
}

// renderMessage assembles one notification message in section style: title
// and identity lines, then one ▍-headed section per configured module in
// order, closing with the amount note. Missing period data, unmatched window
// groups and a nil reset-card count each skip their whole section (spec
// Scenarios); incomplete periods get an ⚠ line and a renderWarnings entry.
func renderMessage(n Notification, d statsData, now time.Time) (string, renderWarnings) {
	var warn renderWarnings
	var b strings.Builder
	b.WriteString(n.Name)
	b.WriteString("\n")
	display, plan := d.DisplayName, d.Plan
	if display == "" {
		display = msgUnknownPlan
	}
	if plan == "" {
		plan = msgUnknownPlan
	}
	fmt.Fprintf(&b, "%s · %s\n\n", display, plan)

	for _, m := range n.Modules {
		if head, ok := moduleHead[m.Kind]; ok {
			start, end, rangeOK := periodRange(m.Kind, m.Period, now, NotificationLocation)
			ps, dataOK := d.Periods[periodKeyOf(m.Kind, m.Period, now)]
			if !rangeOK || !dataOK {
				continue
			}
			scope := "本期累计"
			if m.Period == PeriodPrevious {
				scope = "上一完整周期"
			}
			fmt.Fprintf(&b, "▍%s · %s（%s）\n", head, scope, periodRangeLabel(start, end))
			for _, ch := range ps.Channels {
				fmt.Fprintf(&b, "%s\n", ch.Label)
				fmt.Fprintf(&b, "用量 %s tokens\n", abbreviateTokens(ch.Tokens))
				if ch.ShareKnown {
					fmt.Fprintf(&b, "占比 %.2f%%\n", ch.Share*100)
				} else {
					fmt.Fprintf(&b, "%s\n", msgUnknownShare)
				}
				if ch.CostAvailable {
					fmt.Fprintf(&b, "折算 ≈ $%.2f\n", ch.CostUSD)
				}
			}
			if ps.Incomplete {
				note := fmt.Sprintf("%s~%s 历史数据不完整", ps.MissingFrom, ps.MissingTo)
				fmt.Fprintf(&b, "⚠ %s\n", note)
				warn.Incomplete = append(warn.Incomplete, note)
			}
			b.WriteString("\n")
			continue
		}
		if group, fallback, ok := windowModuleGroup(m.Kind); ok {
			var w *windowStat
			for i := range d.Windows {
				if strings.ToLower(strings.TrimSpace(d.Windows[i].GroupKey)) == group {
					w = &d.Windows[i]
					break
				}
			}
			if w == nil {
				continue
			}
			label := w.Label
			if label == "" {
				label = fallback
			}
			fmt.Fprintf(&b, "▍%s 窗口\n", label)
			if w.WindowUsageAvailable {
				fmt.Fprintf(&b, "已用 %s tokens\n", abbreviateTokens(w.UsedTokens))
			}
			if w.ResetKnown {
				fmt.Fprintf(&b, "重置 %s\n", w.ResetAt.In(NotificationLocation).Format("2006-01-02 15:04"))
				fmt.Fprintf(&b, "还剩 %d 天 %d 小时\n", w.RemainingDays, w.RemainingHours)
			}
			b.WriteString("\n")
			continue
		}
		if m.Kind == ModuleResetCards && d.ResetCards != nil {
			fmt.Fprintf(&b, "▍重置卡（%d 张）\n\n", *d.ResetCards)
		}
	}

	b.WriteString(msgAmountNote)
	return strings.TrimRight(b.String(), "\n"), warn
}

func filterStatsToChannel(d statsData, identity string) statsData {
	out := d
	out.Periods = map[string]periodStats{}
	for k, ps := range d.Periods {
		filtered := ps
		filtered.Channels = nil
		for _, ch := range ps.Channels {
			if ch.Identity == identity || ch.Name == identity {
				filtered.Channels = append(filtered.Channels, ch)
			}
		}
		out.Periods[k] = filtered
	}
	return out
}
