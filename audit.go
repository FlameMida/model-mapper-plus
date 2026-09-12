package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
	_ "time/tzdata"
)

type auditChange struct {
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

type auditEvent struct {
	Version     int                    `json:"version"`
	OperationID string                 `json:"operation_id"`
	Phase       string                 `json:"phase"`
	OccurredAt  time.Time              `json:"occurred_at"`
	Actor       string                 `json:"actor,omitempty"`
	Action      string                 `json:"action,omitempty"`
	ObjectType  string                 `json:"object_type,omitempty"`
	ObjectRef   string                 `json:"object_ref,omitempty"`
	Outcome     string                 `json:"outcome,omitempty"`
	Changed     *bool                  `json:"changed"`
	Changes     map[string]auditChange `json:"changes,omitempty"`
	ErrorCode   string                 `json:"error_code,omitempty"`
}

type auditTicket struct {
	ID        string
	Path      string
	StartedAt time.Time
}

type auditFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

var auditNow = time.Now
var auditOpenFile = func(path string, flag int, perm os.FileMode) (auditFile, error) { return os.OpenFile(path, flag, perm) }
var auditIOMu sync.Mutex
var auditRunning = make(map[string]string)

// A fully written finish can still fail Sync. Preserve that uncertainty for
// this process without changing the append-only journal; after restart the
// reader uses only the complete events that actually survived on disk.
var auditUnconfirmed = make(map[string]string)
var auditLocation = func() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	} // tzdata is embedded above.
	return location
}()

func auditDayPath(statePath, day string) (string, error) {
	parsed, err := time.Parse("2006-01-02", day)
	if err != nil || parsed.Format("2006-01-02") != day {
		return "", errors.New("invalid_audit_date")
	}
	return filepath.Join(filepath.Dir(statePath), "model-mapper-plus-audit", day+".jsonl"), nil
}

func beginAudit(statePath string, event auditEvent) (auditTicket, error) {
	auditIOMu.Lock()
	defer auditIOMu.Unlock()
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return auditTicket{}, err
	}
	now := auditNow().In(auditLocation)
	path, err := auditDayPath(statePath, now.Format("2006-01-02"))
	if err != nil {
		return auditTicket{}, err
	}
	ticket := auditTicket{ID: hex.EncodeToString(id[:]), Path: path, StartedAt: now}
	event.Version, event.OperationID, event.Phase, event.OccurredAt = 1, ticket.ID, "start", now
	if err := appendAuditEvent(path, event); err != nil {
		return auditTicket{}, err
	}
	auditRunning[ticket.ID] = ticket.Path
	return ticket, nil
}

func finishAudit(ticket auditTicket, event auditEvent) error {
	auditIOMu.Lock()
	defer auditIOMu.Unlock()
	defer delete(auditRunning, ticket.ID)
	event.Version, event.OperationID, event.Phase, event.OccurredAt = 1, ticket.ID, "finish", auditNow().In(auditLocation)
	err := appendAuditEvent(ticket.Path, event)
	if err != nil {
		auditUnconfirmed[ticket.ID] = ticket.Path
	}
	return err
}

// Call with auditIOMu held; handlers and configuration changes never run here.
func appendAuditEvent(path string, event auditEvent) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	// Preserve any partial tail, separating it from the next complete event.
	prefix := false
	previous, err := os.Open(path)
	if err == nil {
		info, statErr := previous.Stat()
		if statErr != nil {
			_ = previous.Close()
			return statErr
		}
		if info.Size() > 0 {
			var last [1]byte
			_, err = previous.ReadAt(last[:], info.Size()-1)
			prefix = last[0] != '\n'
		}
		closeErr := previous.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := auditOpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	if prefix {
		raw = append([]byte{'\n'}, raw...)
	}
	raw = append(raw, '\n')
	written, err := file.Write(raw)
	if err != nil {
		return err
	}
	if written != len(raw) {
		return io.ErrShortWrite
	}
	return file.Sync()
}
