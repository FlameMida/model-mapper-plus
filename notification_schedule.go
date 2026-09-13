// notification_schedule.go holds the pure scheduling helpers: the fixed
// operations timezone, "HH:MM:SS" parsing, next-trigger computation per send
// plan kind, and statistics period ranges. All calendar math is anchored to
// NotificationLocation so host/container local time cannot shift boundaries.
package main

import (
	"fmt"
	"time"
)

// NotificationLocation 固定运营时区（与审计日期口径一致）；docker 内 UTC 时
// 不得影响日/月/半年/年边界。
var NotificationLocation = time.FixedZone("CST", 8*3600)

func parseDayTime(s string) (h, m, sec int, ok bool) {
	if len(s) != 8 || s[2] != ':' || s[5] != ':' {
		return 0, 0, 0, false
	}
	if _, err := fmt.Sscanf(s, "%02d:%02d:%02d", &h, &m, &sec); err != nil {
		return 0, 0, 0, false
	}
	return h, m, sec, h >= 0 && h <= 23 && m >= 0 && m <= 59 && sec >= 0 && sec <= 59
}

func nextTrigger(s NotificationSchedule, after time.Time, loc *time.Location) (time.Time, bool) {
	a := after.In(loc)
	switch s.Kind {
	case ScheduleInterval:
		if s.Interval <= 0 {
			return time.Time{}, false
		}
		return a.Add(time.Duration(s.Interval) * time.Second), true
	case ScheduleMonthly:
		h, m, sec, ok := parseDayTime(s.Time)
		if !ok {
			return time.Time{}, false
		}
		candidate := monthBoundary(a.Year(), a.Month(), s.MonthEnd, h, m, sec, loc)
		if !candidate.After(a) {
			y, mo := nextMonth(candidate)
			candidate = monthBoundary(y, mo, s.MonthEnd, h, m, sec, loc)
		}
		return candidate, true
	case ScheduleYearly:
		h, m, sec, ok := parseDayTime(s.Time)
		if !ok || s.Month < 1 || s.Month > 12 || s.Day < 1 || s.Day > 31 {
			return time.Time{}, false
		}
		// 2/29 遇非闰年：该年跳过（time.Date 归一化会产生 3/1，用 Day 回落检测），
		// 不静默改成 2/28；向前找下一个闰年。
		for y := a.Year(); y <= a.Year()+8; y++ {
			t := time.Date(y, time.Month(s.Month), s.Day, h, m, sec, 0, loc)
			if t.Day() != s.Day || !t.After(a) {
				continue
			}
			return t, true
		}
		return time.Time{}, false
	}
	return time.Time{}, false
}

func monthBoundary(year int, month time.Month, monthEnd bool, h, m, sec int, loc *time.Location) time.Time {
	if monthEnd {
		return time.Date(year, month+1, 0, h, m, sec, 0, loc) // day 0 = 当月最后一天
	}
	return time.Date(year, month, 1, h, m, sec, 0, loc)
}

func nextMonth(t time.Time) (int, time.Month) {
	y, m := t.Year(), t.Month()+1
	if m > time.December {
		return y + 1, time.January
	}
	return y, m
}

func periodRange(kind ModuleKind, period PeriodKind, now time.Time, loc *time.Location) (time.Time, time.Time, bool) {
	n := now.In(loc)
	switch kind {
	case ModuleDaily:
		dayStart := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
		if period == PeriodCurrent {
			return dayStart, n, true
		}
		return dayStart.AddDate(0, 0, -1), dayStart.Add(-time.Second), true
	case ModuleMonthly:
		monthStart := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc)
		if period == PeriodCurrent {
			return monthStart, n, true
		}
		// 上月起点：对 1 号做月减法不会触发日归一化，直接回退一个月。
		return monthStart.AddDate(0, -1, 0), monthStart.Add(-time.Second), true
	case ModuleHalfYear:
		var halfStart time.Time
		if n.Month() >= time.July {
			halfStart = time.Date(n.Year(), time.July, 1, 0, 0, 0, 0, loc)
		} else {
			halfStart = time.Date(n.Year(), time.January, 1, 0, 0, 0, 0, loc)
		}
		if period == PeriodCurrent {
			return halfStart, n, true
		}
		var prevStart time.Time
		if n.Month() >= time.July {
			prevStart = time.Date(n.Year(), time.January, 1, 0, 0, 0, 0, loc)
		} else {
			prevStart = time.Date(n.Year()-1, time.July, 1, 0, 0, 0, 0, loc)
		}
		return prevStart, halfStart.Add(-time.Second), true
	case ModuleYearly:
		yearStart := time.Date(n.Year(), time.January, 1, 0, 0, 0, 0, loc)
		if period == PeriodCurrent {
			return yearStart, n, true
		}
		return time.Date(n.Year()-1, time.January, 1, 0, 0, 0, 0, loc), yearStart.Add(-time.Second), true
	}
	return time.Time{}, time.Time{}, false
}
