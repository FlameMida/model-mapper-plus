// notification_schedule_test.go covers the pure scheduling functions:
// next-trigger computation and statistics period ranges, all anchored to the
// fixed operations timezone (NotificationLocation).
package main

import (
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation(time.RFC3339, s, NotificationLocation)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return v
}

func TestNextTriggerInterval(t *testing.T) {
	after := mustTime(t, "2026-09-13T09:00:00+08:00")
	next, ok := nextTrigger(NotificationSchedule{Kind: ScheduleInterval, Interval: 3600}, after, NotificationLocation)
	if !ok || !next.Equal(after.Add(time.Hour)) {
		t.Fatalf("got %v ok=%v", next, ok)
	}
}

func TestNextTriggerMonthlyMonthEnd(t *testing.T) {
	after := mustTime(t, "2026-09-15T00:00:00+08:00")
	next, ok := nextTrigger(NotificationSchedule{Kind: ScheduleMonthly, MonthEnd: true, Time: "23:59:59"}, after, NotificationLocation)
	// 9 月有 30 天
	if !ok || next.Format("2006-01-02 15:04:05") != "2026-09-30 23:59:59" {
		t.Fatalf("month end, got %s ok=%v", next, ok)
	}
}

func TestNextTriggerYearlyLeapDaySkipsNonLeap(t *testing.T) {
	after := mustTime(t, "2027-01-01T00:00:00+08:00")
	next, _ := nextTrigger(NotificationSchedule{Kind: ScheduleYearly, Month: 2, Day: 29, Time: "09:00:00"}, after, NotificationLocation)
	// 2027 非闰年 → 不静默改成 2/28，跳到 2028-02-29
	if next.Format("2006-01-02 15:04:05") != "2028-02-29 09:00:00" {
		t.Fatalf("leap day skip, got %s", next)
	}
}

func TestPeriodRangeMonthlyPrevious(t *testing.T) {
	now := mustTime(t, "2026-09-13T17:00:00+08:00")
	start, end, ok := periodRange(ModuleMonthly, PeriodPrevious, now, NotificationLocation)
	if !ok || start.Format("2006-01-02") != "2026-08-01" || end.Format("2006-01-02") != "2026-08-31" {
		t.Fatalf("previous month, got %s ~ %s ok=%v", start, end, ok)
	}
}

func TestPeriodRangeHalfYearCurrentBoundary(t *testing.T) {
	july := mustTime(t, "2026-07-05T10:00:00+08:00")
	start, _, ok := periodRange(ModuleHalfYear, PeriodCurrent, july, NotificationLocation)
	if !ok || start.Format("2006-01-02") != "2026-07-01" {
		t.Fatalf("half-year boundary, got %s", start)
	}
	previousStart, previousEnd, _ := periodRange(ModuleHalfYear, PeriodPrevious, july, NotificationLocation)
	if previousStart.Format("2006-01-02") != "2026-01-01" || previousEnd.Format("2006-01-02") != "2026-06-30" {
		t.Fatalf("previous half-year, got %s ~ %s", previousStart, previousEnd)
	}
}

func TestParseDayTimeInvalid(t *testing.T) {
	for _, s := range []string{"24:00:00", "9:00", "aa"} {
		if _, _, _, ok := parseDayTime(s); ok {
			t.Fatalf("parseDayTime(%q) should be invalid", s)
		}
	}
}
