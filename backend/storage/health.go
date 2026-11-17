package storage

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// healthTracker is a per-storage circuit breaker. After failureThreshold
// consecutive failures a storage is skipped for a back-off window, so an
// unreachable node cannot stall image reads or the background workers.
// State is mirrored to the storages table for the admin UI and for other
// processes sharing the database.
type healthTracker struct {
	exec     func(query string, args ...any) // persists state; replaced in tests
	mu       sync.Mutex
	failures map[uint]int
	openTill map[uint]time.Time
	now      func() time.Time
}

const failureThreshold = 3

func newHealthTracker(db interface {
	Exec(string, ...any) (sql.Result, error)
}) *healthTracker {
	exec := func(query string, args ...any) { _, _ = db.Exec(query, args...) }
	return &healthTracker{exec: exec, failures: map[uint]int{}, openTill: map[uint]time.Time{}, now: time.Now}
}

// Available reports whether requests may be sent to the storage now.
func (h *healthTracker) Available(id uint, persisted *time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if until, ok := h.openTill[id]; ok && now.Before(until) {
		return false
	}
	return persisted == nil || !now.Before(*persisted)
}

// RetryAt returns when an open breaker allows the next attempt.
func (h *healthTracker) RetryAt(id uint) time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.openTill[id]
}

func backoffFor(failures int) time.Duration {
	steps := failures - failureThreshold
	if steps < 0 {
		steps = 0
	}
	if steps > 5 {
		steps = 5
	}
	return time.Duration(1<<uint(steps)) * time.Minute // 1, 2, 4 … 32 → capped below
}

// Fail records an I/O failure. Missing objects are not storage failures.
func (h *healthTracker) Fail(id uint, cause error) {
	// Missing or damaged objects are data problems, not connectivity ones.
	if id == 0 || cause == nil || errors.Is(cause, ErrObjectNotFound) || errors.Is(cause, errChecksum) || errors.Is(cause, context.Canceled) {
		return
	}
	h.mu.Lock()
	h.failures[id]++
	failures := h.failures[id]
	var until time.Time
	if failures >= failureThreshold {
		wait := backoffFor(failures)
		if wait > 30*time.Minute {
			wait = 30 * time.Minute
		}
		until = h.now().Add(wait)
		h.openTill[id] = until
	}
	h.mu.Unlock()
	message := truncateError(cause)
	if len(message) > 500 {
		message = message[:500]
	}
	if until.IsZero() {
		h.exec(`UPDATE storages SET health_error=?, health_checked_at=NOW() WHERE id=?`, message, id)
		return
	}
	h.exec(`UPDATE storages SET health='offline', health_error=?, health_checked_at=NOW(), offline_until=? WHERE id=?`, message, until, id)
}

// OK records a successful operation and closes the breaker.
func (h *healthTracker) OK(id uint) {
	if id == 0 {
		return
	}
	h.mu.Lock()
	changed := h.failures[id] != 0 || !h.openTill[id].IsZero()
	delete(h.failures, id)
	delete(h.openTill, id)
	h.mu.Unlock()
	if changed {
		h.exec(`UPDATE storages SET health='ok', health_error='', health_checked_at=NOW(), offline_until=NULL WHERE id=?`, id)
		return
	}
	// Cheap periodic heartbeat for the UI.
	h.exec(`UPDATE storages SET health='ok', health_error='', health_checked_at=NOW(), offline_until=NULL
		WHERE id=? AND (health<>'ok' OR health_checked_at IS NULL OR health_checked_at < DATE_SUB(NOW(), INTERVAL 5 MINUTE))`, id)
}

// Reset clears the breaker, for example after a successful manual probe.
func (h *healthTracker) Reset(id uint) {
	h.mu.Lock()
	delete(h.failures, id)
	delete(h.openTill, id)
	h.mu.Unlock()
}

func truncateError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 1000 {
		message = message[:1000]
	}
	return message
}
