package main

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *notificationStore {
	t.Helper()
	s, err := openNotificationStore(filepath.Join(t.TempDir(), "notif.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertJobMergesSamePeriodPending(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	base := notificationJob{ID: "j1", KeyFingerprint: "fp1", NotificationID: "global", Revision: 3,
		Platform: PlatformFeishu, PeriodKey: "daily:2026-09-13", State: jobPending,
		Payload: []byte("v1"), NextAttempt: now, CreatedAt: now}
	if _, err := s.UpsertJob(base); err != nil {
		t.Fatalf("upsert1: %v", err)
	}
	later := base
	later.ID = "j2"
	later.Payload = []byte("v2-latest")
	later.Revision = 7
	merged, err := s.UpsertJob(later)
	if err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	if merged != 2 {
		t.Fatalf("want merged count 2, got %d", merged)
	}
	due, _ := s.ClaimDueJobs(now.Add(time.Minute), 10)
	if len(due) != 1 || string(due[0].Payload) != "v2-latest" || due[0].MergedCount != 2 {
		t.Fatalf("want single latest job, got %+v", due)
	}
	if due[0].Revision != 7 {
		t.Fatalf("merge must refresh Revision to caller-supplied 7, got %d", due[0].Revision)
	}
}

func TestDifferentPeriodJobsDoNotMerge(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	a := notificationJob{ID: "a", KeyFingerprint: "fp", NotificationID: "n", Platform: PlatformFeishu, PeriodKey: "daily:2026-09-13", State: jobPending, NextAttempt: now, CreatedAt: now}
	b := a
	b.ID = "b"
	b.PeriodKey = "daily:2026-09-14"
	if _, err := s.UpsertJob(a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertJob(b); err != nil {
		t.Fatal(err)
	}
	due, _ := s.ClaimDueJobs(now.Add(time.Minute), 10)
	if len(due) != 2 {
		t.Fatalf("different periods must not merge, got %d", len(due))
	}
}

func TestRecoverOnBootMarksSendingUnknown(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	j := notificationJob{ID: "j", KeyFingerprint: "fp", NotificationID: "n", Platform: PlatformFeishu, PeriodKey: "p", State: jobPending, NextAttempt: now, CreatedAt: now}
	if _, err := s.UpsertJob(j); err != nil {
		t.Fatal(err)
	}
	due, _ := s.ClaimDueJobs(now.Add(time.Minute), 10)
	if len(due) != 1 || due[0].State != jobSending {
		t.Fatalf("claim: %+v", due)
	}
	if err := s.RecoverOnBoot(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	rows, err := s.Deliveries(DeliveryFilter{Limit: 10})
	if err != nil || len(rows) != 1 || rows[0].Outcome != deliveryUnknown {
		t.Fatalf("want unknown delivery after recover, got %v %v", rows, err)
	}
}

func TestSnapshotPutList(t *testing.T) {
	s := openTestStore(t)
	fp := keyFingerprint("k1")
	rows := []snapshotRecord{
		{AuthIndex: "a1", Channel: "ch", PeriodKey: "daily:2026-09-13", Tokens: 100, CostUSD: 0.5, CostAvailable: true, Plan: "pro"},
		{AuthIndex: "a2", Channel: "ch", PeriodKey: "daily:2026-09-14", Tokens: 200},
	}
	if err := s.PutSnapshots("k1", rows); err != nil {
		t.Fatalf("put: %v", err)
	}
	all, err := s.SnapshotsFor(fp, "daily:")
	if err != nil || len(all) != 2 {
		t.Fatalf("want 2 rows for fingerprint %s, got %d err %v", fp, len(all), err)
	}
	day, err := s.SnapshotsFor(fp, "daily:2026-09-13")
	if err != nil || len(day) != 1 {
		t.Fatalf("prefix filter want 1 row, got %d err %v", len(day), err)
	}
	row := day[0]
	if row.AuthIndex != "a1" || row.Tokens != 100 || !row.CostAvailable || row.CostUSD != 0.5 || row.Plan != "pro" {
		t.Fatalf("snapshot roundtrip mismatch: %+v", row)
	}
	if row.KeyFingerprint != fp {
		t.Fatalf("store must stamp key fingerprint, got %q want %q", row.KeyFingerprint, fp)
	}
	other, err := s.SnapshotsFor(keyFingerprint("k2"), "")
	if err != nil || len(other) != 0 {
		t.Fatalf("unrelated fingerprint must be empty, got %d err %v", len(other), err)
	}
}
