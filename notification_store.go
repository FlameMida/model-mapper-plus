package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	jobPending    = "pending"
	jobSending    = "sending"
	jobSent       = "sent"
	jobFailed     = "failed"
	jobUnknown    = "unknown"
	jobSuperseded = "superseded"

	deliveryAccepted = "accepted"
	deliveryFailed   = "failed"
	deliveryUnknown  = "unknown"

	revisionStoreKey = "revision"
)

var (
	bucketRevision   = []byte("config_revision")
	bucketJobs       = []byte("jobs")
	bucketDeliveries = []byte("deliveries")
	bucketSnapshots  = []byte("snapshots")
)

// keyFingerprint returns the stable 16-hex-char fingerprint of an API key.
func keyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:16]
}

type notificationJob struct {
	ID             string
	KeyFingerprint string
	NotificationID string
	Platform       PlatformKind
	PeriodKey      string
	State          string
	Payload        []byte
	MergedCount    int
	Attempts       int
	Revision       uint64
	NextAttempt    time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type deliveryRecord struct {
	ID             string
	JobID          string
	KeyFingerprint string
	NotificationID string
	Platform       PlatformKind
	PeriodKey      string
	Outcome        string
	ErrorCode      string
	Detail         string
	CreatedAt      time.Time
}

type snapshotRecord struct {
	KeyFingerprint string
	AuthIndex      string
	Channel        string
	PeriodKey      string
	Tokens         int64
	CostUSD        float64
	CostAvailable  bool
	Plan           string
	ResetCards     *int
	CoverageStart  string
	CoverageEnd    string
	FetchedAt      string
	Incomplete     bool
}

// DeliveryFilter selects delivery records; zero-value fields are not filtered.
type DeliveryFilter struct {
	KeyFingerprint, NotificationID string
	Platform                       PlatformKind
	Outcome                        string
	Limit                          int
}

type notificationStore struct{ db *bolt.DB }

func openNotificationStore(path string) (*notificationStore, error) {
	// The 3s file-lock timeout turns concurrent-open conflicts into a controlled
	// unavailable error instead of an indefinite block (hot-reload contract).
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open notification store: %w", err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bucketRevision, bucketJobs, bucketDeliveries, bucketSnapshots} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("create bucket %s: %w", name, err)
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init notification store: %w", err)
	}
	return &notificationStore{db: db}, nil
}

// Close releases the underlying bbolt file lock.
func (s *notificationStore) Close() error { return s.db.Close() }

// Revision returns the current config revision (0 when never bumped).
func (s *notificationStore) Revision() uint64 {
	var rev uint64
	_ = s.db.View(func(tx *bolt.Tx) error {
		rev = readRevision(tx)
		return nil
	})
	return rev
}

// BumpRevision increments and persists the config revision in one write transaction.
func (s *notificationStore) BumpRevision() (uint64, error) {
	var rev uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketRevision)
		if b == nil {
			return fmt.Errorf("bucket %s missing", bucketRevision)
		}
		rev = readRevision(tx) + 1
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], rev)
		return b.Put([]byte(revisionStoreKey), buf[:])
	})
	if err != nil {
		return 0, err
	}
	return rev, nil
}

func readRevision(tx *bolt.Tx) uint64 {
	b := tx.Bucket(bucketRevision)
	if b == nil {
		return 0
	}
	raw := b.Get([]byte(revisionStoreKey))
	if len(raw) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(raw)
}

// jobKey is the merge dimension: same fingerprint/notification/platform/period
// collapses onto one stored job entry.
func jobKey(j notificationJob) string {
	return j.KeyFingerprint + "/" + j.NotificationID + "/" + string(j.Platform) + "/" + j.PeriodKey
}

// UpsertJob inserts a job, merging into an existing pending job of the same
// dimension (payload/next-attempt refreshed, merged count bumped). The stored
// job is stamped with the config revision at enqueue time.
func (s *notificationStore) UpsertJob(j notificationJob) (int, error) {
	merged := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketJobs)
		if b == nil {
			return fmt.Errorf("bucket %s missing", bucketJobs)
		}
		key := []byte(jobKey(j))
		if raw := b.Get(key); raw != nil {
			var existing notificationJob
			if json.Unmarshal(raw, &existing) == nil && existing.State == jobPending {
				existing.Payload = j.Payload
				existing.NextAttempt = j.NextAttempt
				existing.MergedCount++
				existing.UpdatedAt = time.Now().UTC()
				merged = existing.MergedCount
				updated, err := json.Marshal(existing)
				if err != nil {
					return err
				}
				return b.Put(key, updated)
			}
		}
		j.MergedCount = 1
		j.UpdatedAt = time.Now().UTC()
		j.Revision = readRevision(tx)
		if j.CreatedAt.IsZero() {
			j.CreatedAt = j.UpdatedAt
		}
		merged = 1
		raw, err := json.Marshal(j)
		if err != nil {
			return err
		}
		return b.Put(key, raw)
	})
	if err != nil {
		return 0, err
	}
	return merged, nil
}

// ClaimDueJobs flips up to limit due pending jobs to sending and returns them
// in the same write transaction.
func (s *notificationStore) ClaimDueJobs(now time.Time, limit int) ([]notificationJob, error) {
	var claimed []notificationJob
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketJobs)
		if b == nil {
			return fmt.Errorf("bucket %s missing", bucketJobs)
		}
		// bbolt forbids bucket mutation while iterating: collect keys read-only first.
		var due [][]byte
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var j notificationJob
			if json.Unmarshal(v, &j) != nil {
				continue
			}
			if j.State != jobPending || j.NextAttempt.After(now) {
				continue
			}
			due = append(due, append([]byte(nil), k...))
			if limit > 0 && len(due) >= limit {
				break
			}
		}
		for _, key := range due {
			raw := b.Get(key)
			if raw == nil {
				continue
			}
			var j notificationJob
			if json.Unmarshal(raw, &j) != nil {
				continue
			}
			j.State = jobSending
			j.UpdatedAt = time.Now().UTC()
			updated, err := json.Marshal(j)
			if err != nil {
				return err
			}
			if err := b.Put(key, updated); err != nil {
				return err
			}
			claimed = append(claimed, j)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func jobStateForOutcome(outcome string) (string, error) {
	switch outcome {
	case deliveryAccepted:
		return jobSent, nil
	case deliveryFailed:
		return jobFailed, nil
	case deliveryUnknown:
		return jobUnknown, nil
	default:
		return "", fmt.Errorf("unknown delivery outcome %q", outcome)
	}
}

func appendDelivery(tx *bolt.Tx, rec deliveryRecord) error {
	b := tx.Bucket(bucketDeliveries)
	if b == nil {
		return fmt.Errorf("bucket %s missing", bucketDeliveries)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	key := rec.CreatedAt.UTC().Format("20060102150405.000000000") + "/" + rec.ID
	return b.Put([]byte(key), raw)
}

// FinishJob moves a sending job to its terminal state and records the
// delivery outcome in the same transaction.
func (s *notificationStore) FinishJob(id, outcome, errCode, detail string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		jb := tx.Bucket(bucketJobs)
		if jb == nil {
			return fmt.Errorf("bucket %s missing", bucketJobs)
		}
		var jobKeyRaw, jobRaw []byte
		c := jb.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var j notificationJob
			if json.Unmarshal(v, &j) == nil && j.ID == id {
				jobKeyRaw = append([]byte(nil), k...)
				jobRaw = v
				break
			}
		}
		if jobRaw == nil {
			return fmt.Errorf("job %s not found", id)
		}
		state, err := jobStateForOutcome(outcome)
		if err != nil {
			return err
		}
		var j notificationJob
		if err := json.Unmarshal(jobRaw, &j); err != nil {
			return err
		}
		j.State = state
		j.UpdatedAt = time.Now().UTC()
		updated, err := json.Marshal(j)
		if err != nil {
			return err
		}
		if err := jb.Put(jobKeyRaw, updated); err != nil {
			return err
		}
		return appendDelivery(tx, deliveryRecord{
			ID:             fmt.Sprintf("%d-%s", time.Now().UnixNano(), id),
			JobID:          j.ID,
			KeyFingerprint: j.KeyFingerprint,
			NotificationID: j.NotificationID,
			Platform:       j.Platform,
			PeriodKey:      j.PeriodKey,
			Outcome:        outcome,
			ErrorCode:      errCode,
			Detail:         detail,
			CreatedAt:      time.Now().UTC(),
		})
	})
}

// RecoverOnBoot downgrades every in-flight (sending) job to unknown and writes
// one unknown delivery record per job, so a restart can never lose the fact
// that a send outcome is indeterminate.
func (s *notificationStore) RecoverOnBoot() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		jb := tx.Bucket(bucketJobs)
		if jb == nil {
			return fmt.Errorf("bucket %s missing", bucketJobs)
		}
		type inflight struct {
			key []byte
			job notificationJob
		}
		var inflights []inflight
		c := jb.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var j notificationJob
			if json.Unmarshal(v, &j) != nil || j.State != jobSending {
				continue
			}
			inflights = append(inflights, inflight{key: append([]byte(nil), k...), job: j})
		}
		now := time.Now().UTC()
		for _, it := range inflights {
			it.job.State = jobUnknown
			it.job.UpdatedAt = now
			updated, err := json.Marshal(it.job)
			if err != nil {
				return err
			}
			if err := jb.Put(it.key, updated); err != nil {
				return err
			}
			if err := appendDelivery(tx, deliveryRecord{
				ID:             "recover-" + it.job.ID,
				JobID:          it.job.ID,
				KeyFingerprint: it.job.KeyFingerprint,
				NotificationID: it.job.NotificationID,
				Platform:       it.job.Platform,
				PeriodKey:      it.job.PeriodKey,
				Outcome:        deliveryUnknown,
				ErrorCode:      "restart_during_send",
				Detail:         "plugin restart interrupted in-flight delivery",
				CreatedAt:      now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// Deliveries lists delivery records newest-first, honoring non-zero filter
// fields and the limit (Limit <= 0 means unbounded).
func (s *notificationStore) Deliveries(f DeliveryFilter) ([]deliveryRecord, error) {
	var rows []deliveryRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDeliveries)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var rec deliveryRecord
			if json.Unmarshal(v, &rec) != nil {
				continue
			}
			if f.KeyFingerprint != "" && rec.KeyFingerprint != f.KeyFingerprint {
				continue
			}
			if f.NotificationID != "" && rec.NotificationID != f.NotificationID {
				continue
			}
			if f.Platform != "" && rec.Platform != f.Platform {
				continue
			}
			if f.Outcome != "" && rec.Outcome != f.Outcome {
				continue
			}
			rows = append(rows, rec)
			if f.Limit > 0 && len(rows) >= f.Limit {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// PutSnapshots stores snapshot rows under fingerprint/periodKey/seq keys,
// stamping each row with the fingerprint of the given raw API key.
func (s *notificationStore) PutSnapshots(key string, rows []snapshotRecord) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSnapshots)
		if b == nil {
			return fmt.Errorf("bucket %s missing", bucketSnapshots)
		}
		fp := keyFingerprint(key)
		seq := time.Now().UnixNano()
		for _, row := range rows {
			row.KeyFingerprint = fp
			raw, err := json.Marshal(row)
			if err != nil {
				return err
			}
			bk := fp + "/" + row.PeriodKey + "/" + strconv.FormatInt(seq, 10)
			seq++
			if err := b.Put([]byte(bk), raw); err != nil {
				return err
			}
		}
		return nil
	})
}

// SnapshotsFor scans snapshot rows whose key starts with
// fingerprint/periodKeyPrefix/, newest sequence first.
func (s *notificationStore) SnapshotsFor(fingerprint, periodKeyPrefix string) ([]snapshotRecord, error) {
	var rows []snapshotRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketSnapshots)
		if b == nil {
			return nil
		}
		prefix := []byte(fingerprint + "/" + periodKeyPrefix)
		c := b.Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			if !bytes.HasPrefix(k, prefix) {
				continue
			}
			var rec snapshotRecord
			if json.Unmarshal(v, &rec) != nil {
				continue
			}
			rows = append(rows, rec)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}
