package storage

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"zhuapic/models"
	"zhuapic/utils"
)

const maxCopyAttempts = 10

// Start runs the background workers until ctx is cancelled:
//   - one lane per storage that copies, verifies and deletes objects on that
//     storage, so a slow or offline storage only delays its own work;
//   - a maintenance loop for expiry, purging, backups and operations.
func (s *Service) Start(ctx context.Context) {
	var wg sync.WaitGroup
	lanes := map[uint]context.CancelFunc{}
	syncLanes := func() {
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM storages`)
		if err != nil {
			return
		}
		present := map[uint]bool{}
		for rows.Next() {
			var id uint
			if rows.Scan(&id) == nil {
				present[id] = true
			}
		}
		rows.Close()
		for id := range present {
			if _, ok := lanes[id]; !ok {
				laneCtx, cancel := context.WithCancel(ctx)
				lanes[id] = cancel
				wg.Add(1)
				go func(id uint) { defer wg.Done(); s.lane(laneCtx, id) }(id)
			}
		}
		for id, cancel := range lanes {
			if !present[id] {
				cancel()
				delete(lanes, id)
			}
		}
	}
	wg.Add(2)
	go func() { defer wg.Done(); s.maintenance(ctx) }()
	go func() { defer wg.Done(); s.backupRunner(ctx) }()
	syncLanes()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			for _, cancel := range lanes {
				cancel()
			}
			wg.Wait()
			return
		case <-ticker.C:
			syncLanes()
		}
	}
}

// waitForWork sleeps until the timeout, a Wake broadcast or shutdown.
func (s *Service) waitForWork(ctx context.Context, d time.Duration) {
	wake := s.wakeChannel()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	case <-wake:
	}
}

func (s *Service) lane(ctx context.Context, id uint) {
	for ctx.Err() == nil {
		if !utils.SettingBool(s.db, "replica_worker_enabled", true) {
			s.waitForWork(ctx, 15*time.Second)
			continue
		}
		st, err := s.Get(ctx, id)
		if err != nil {
			if ctx.Err() == nil {
				s.waitForWork(ctx, 10*time.Second)
			}
			continue
		}
		if !s.health.Available(id, st.OfflineUntil) {
			wait := 30 * time.Second
			if until := s.health.RetryAt(id); !until.IsZero() && time.Until(until) < wait {
				wait = time.Until(until)
			} else if st.OfflineUntil != nil && time.Until(*st.OfflineUntil) < wait {
				wait = time.Until(*st.OfflineUntil)
			}
			s.waitForWork(ctx, wait+time.Second)
			continue
		}
		worked := false
		if st.Writable() {
			worked, err = s.processCopy(ctx, st)
		}
		if !worked && err == nil && st.Status != StatusPaused {
			worked, err = s.processGC(ctx, st)
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("storage %d: %v", id, err)
		}
		if !worked {
			s.waitForWork(ctx, 3*time.Second)
		}
	}
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<uint(attempt-1)) * 30 * time.Second
}

// processCopy claims one copy on st that needs writing or verifying.
func (s *Service) processCopy(ctx context.Context, st *Storage) (bool, error) {
	var image models.Image
	var state, key, expected string
	var attempts int
	err := s.db.QueryRowContext(ctx, `SELECT c.image_id, c.state, c.object_key, c.attempts, i.public_name, i.mime_type, i.size, i.created_at, i.checksum_sha256
		FROM image_copies c JOIN images i ON i.id=c.image_id
		WHERE c.storage_id=? AND c.state IN ('pending','failed','missing','verifying') AND c.attempts < ?
		AND (c.next_attempt_at IS NULL OR c.next_attempt_at <= NOW()) AND (c.lease_until IS NULL OR c.lease_until < NOW())
		AND (i.deleted_at IS NULL OR c.state='verifying')
		ORDER BY c.state='verifying', c.updated_at LIMIT 1`, st.ID, maxCopyAttempts).
		Scan(&image.ID, &state, &key, &attempts, &image.PublicName, &image.MimeType, &image.Size, &image.CreatedAt, &expected)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	token, err := utils.RandomHex(16)
	if err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE image_copies SET lease_token=?, lease_until=DATE_ADD(NOW(), INTERVAL 30 MINUTE), attempts=attempts+1
		WHERE image_id=? AND storage_id=? AND state=? AND (lease_until IS NULL OR lease_until < NOW())`, token, image.ID, st.ID, state)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return true, nil // another worker claimed it
	}
	attempts++
	opts := s.Options(st)
	if key == "" {
		key = s.manager.ReplicaKey(opts, st.Mode(), image.CreatedAt, image.PublicName)
	}
	opCtx, cancel := context.WithTimeout(ctx, 2*opTimeout(image.Size))
	defer cancel()
	var checksum string
	var destErr, srcErr error
	if state == "verifying" {
		checksum, destErr = s.readBack(opCtx, st, opts, key, image.Size)
		if destErr == nil && expected != "" && checksum != expected {
			destErr = errChecksum
		}
	} else {
		checksum, srcErr, destErr = s.copyTo(opCtx, st, opts, key, &image, expected)
	}
	if srcErr == nil && destErr == nil {
		s.health.OK(st.ID)
		_, err = s.db.ExecContext(ctx, `UPDATE image_copies SET state='ready', object_key=?, checksum_sha256=?, size=?, attempts=0, last_error='',
			next_attempt_at=NULL, lease_token='', lease_until=NULL, verified_at=NOW() WHERE image_id=? AND storage_id=? AND lease_token=?`,
			key, checksum, image.Size, image.ID, st.ID, token)
		if err == nil && expected == "" {
			_, err = s.db.ExecContext(ctx, `UPDATE images SET checksum_sha256=? WHERE id=? AND checksum_sha256=''`, checksum, image.ID)
		}
		return true, err
	}
	failure := destErr
	next := "failed"
	if srcErr != nil {
		failure = srcErr // the destination is fine; wait for a source
	} else {
		s.health.Fail(st.ID, destErr)
		if state == "verifying" && errors.Is(destErr, ErrObjectNotFound) {
			next = "missing"
		} else if state == "verifying" && !errors.Is(destErr, errChecksum) {
			next = "verifying" // could not reach the storage; verify later
		}
	}
	if next == "failed" && state == "missing" {
		next = "missing"
	}
	_, err = s.db.ExecContext(ctx, `UPDATE image_copies SET state=?, last_error=?, next_attempt_at=?, lease_token='', lease_until=NULL
		WHERE image_id=? AND storage_id=? AND lease_token=?`, next, truncateError(failure), time.Now().Add(retryDelay(attempts)), image.ID, st.ID, token)
	return true, err
}

// copyTo writes the image to st from the best other copy and reads it back.
func (s *Service) copyTo(ctx context.Context, st *Storage, opts Options, key string, image *models.Image, expected string) (string, error, error) {
	if st.Mode() == "local" {
		if err := s.checkDisk(opts); err != nil {
			return "", nil, err
		}
	}
	staged, hash, err := s.openSource(ctx, image, expected, st.ID)
	if err != nil {
		return "", err, nil
	}
	defer func() { staged.Close(); os.Remove(staged.Name()) }()
	if _, err = staged.Seek(0, 0); err != nil {
		return "", err, nil
	}
	if err = s.manager.Put(ctx, opts, st.Mode(), key, image.MimeType, image.Size, staged, hash); err != nil {
		return "", nil, err
	}
	written, err := s.readBack(ctx, st, opts, key, image.Size)
	if err != nil {
		return "", nil, err
	}
	if written != hash {
		return "", nil, errChecksum
	}
	return hash, nil, nil
}

func (s *Service) readBack(ctx context.Context, st *Storage, opts Options, key string, size int64) (string, error) {
	reader, err := s.manager.Open(ctx, opts, st.Mode(), key)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	staged, hash, err := stageReplica(reader, size)
	if err != nil {
		return "", err
	}
	staged.Close()
	os.Remove(staged.Name())
	return hash, nil
}

// processGC deletes one queued object from st.
func (s *Service) processGC(ctx context.Context, st *Storage) (bool, error) {
	var id uint
	var key string
	var attempts int
	err := s.db.QueryRowContext(ctx, `SELECT id, object_key, attempts FROM storage_gc WHERE storage_id=? AND (next_attempt_at IS NULL OR next_attempt_at <= NOW()) ORDER BY id LIMIT 1`, st.ID).Scan(&id, &key, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE storage_gc SET next_attempt_at=DATE_ADD(NOW(), INTERVAL 10 MINUTE), attempts=attempts+1 WHERE id=? AND attempts=?`, id, attempts)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return true, nil
	}
	// Never delete an object that a live copy still points at.
	var live int
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_copies WHERE storage_id=? AND object_key=?`, st.ID, key).Scan(&live); err != nil {
		return false, err
	}
	if live == 0 {
		opCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err = s.manager.DeleteObject(opCtx, s.Options(st), st.Mode(), key)
		cancel()
		if err != nil {
			s.health.Fail(st.ID, err)
			_, err = s.db.ExecContext(ctx, `UPDATE storage_gc SET last_error=?, next_attempt_at=? WHERE id=?`, truncateError(err), time.Now().Add(retryDelay(attempts+1)), id)
			return true, err
		}
		s.health.OK(st.ID)
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM storage_gc WHERE id=?`, id)
	return true, err
}

func (s *Service) maintenance(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	lastBackfill := time.Time{}
	for {
		if utils.SettingBool(s.db, "replica_worker_enabled", true) && time.Since(lastBackfill) > 5*time.Minute {
			_ = s.ensureBackupCopies(ctx, 0)
			lastBackfill = time.Now()
		}
		_ = s.expireDueImages(ctx)
		_ = s.purgeDueImages(ctx)
		_ = s.progressOperations(ctx)
		_ = s.scheduleDatabaseBackup(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) expireDueImages(ctx context.Context) error {
	days := utils.SettingInt64(s.db, "recycle_bin_days", 7, 1, 30)
	_, err := s.db.ExecContext(ctx, `UPDATE images SET deleted_at=NOW(), delete_after=DATE_ADD(NOW(), INTERVAL ? DAY)
		WHERE deleted_at IS NULL AND expires_at IS NOT NULL AND expires_at <= NOW() LIMIT 100`, days)
	return err
}

func (s *Service) purgeDueImages(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM images WHERE deleted_at IS NOT NULL AND delete_after <= NOW() ORDER BY delete_after LIMIT 50`)
	if err != nil {
		return err
	}
	var ids []uint
	for rows.Next() {
		var id uint
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err := s.PurgeImage(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
