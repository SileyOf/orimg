package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

var cacheImageName = regexp.MustCompile(`^[a-f0-9]{24}\.[a-z0-9]+$`)

type CacheFile struct {
	Name      string    `json:"name"`
	Bytes     int64     `json:"bytes"`
	ExpiresAt time.Time `json:"expires_at"`
}

type CacheInventory struct {
	Directory    string      `json:"directory"`
	Files        int64       `json:"files"`
	Bytes        int64       `json:"bytes"`
	ExpiredFiles int64       `json:"expired_files"`
	ExpiredBytes int64       `json:"expired_bytes"`
	TTLSeconds   int64       `json:"ttl_seconds"`
	ScannedAt    time.Time   `json:"scanned_at"`
	Partial      bool        `json:"partial"`
	Status       string      `json:"status"`
	Largest      []CacheFile `json:"largest"`
}

// Cache objects live at the root. Never recurse into backups, staging directories or symlinks.
func visitCache(ctx context.Context, directory string, limit int, visit func(string, os.FileInfo)) (bool, error) {
	f, err := os.Open(directory)
	if err != nil {
		return false, err
	}
	defer f.Close()
	seen := 0
	for {
		entries, err := f.ReadDir(256)
		for _, entry := range entries {
			if ctx.Err() != nil || (limit > 0 && seen >= limit) {
				return true, nil
			}
			seen++
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !cacheImageName.MatchString(entry.Name()) {
				continue
			}
			info, statErr := entry.Info()
			if statErr != nil {
				if os.IsNotExist(statErr) {
					continue
				}
				return true, statErr
			}
			if info.Mode().IsRegular() {
				visit(filepath.Join(directory, entry.Name()), info)
			}
		}
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return true, err
		}
	}
}

func scanCache(ctx context.Context, directory string, ttl time.Duration, now time.Time, limit int) CacheInventory {
	result := CacheInventory{Directory: directory, TTLSeconds: int64(ttl / time.Second), ScannedAt: now, Status: "ready", Largest: []CacheFile{}}
	partial, err := visitCache(ctx, directory, limit, func(path string, info os.FileInfo) {
		result.Files++
		result.Bytes += info.Size()
		expiry := info.ModTime().Add(ttl)
		if !expiry.After(now) {
			result.ExpiredFiles++
			result.ExpiredBytes += info.Size()
		}
		if len(result.Largest) < 20 || info.Size() > result.Largest[len(result.Largest)-1].Bytes {
			result.Largest = append(result.Largest, CacheFile{Name: filepath.Base(path), Bytes: info.Size(), ExpiresAt: expiry})
			sort.Slice(result.Largest, func(i, j int) bool { return result.Largest[i].Bytes > result.Largest[j].Bytes })
			if len(result.Largest) > 20 {
				result.Largest = result.Largest[:20]
			}
		}
	})
	result.Partial = partial
	if os.IsNotExist(err) {
		result.Status = "empty"
	} else if err != nil {
		result.Status = "unavailable"
		result.Partial = true
	}
	return result
}

func (m *Manager) CacheInventory(ctx context.Context, opts Options, ttl time.Duration) CacheInventory {
	opts = m.normalizeOptions(opts)
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if m.cacheStats.Directory == opts.CacheDir && m.cacheStats.TTLSeconds == int64(ttl/time.Second) && time.Since(m.cacheStats.ScannedAt) < 30*time.Second {
		return m.cacheStats
	}
	scanCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	m.cacheStats = scanCache(scanCtx, opts.CacheDir, ttl, time.Now(), 100000)
	return m.cacheStats
}

func cleanCache(ctx context.Context, directory string, ttl time.Duration, now time.Time) {
	_, _ = visitCache(ctx, directory, 0, func(path string, info os.FileInfo) {
		if !info.ModTime().Add(ttl).After(now) {
			_ = os.Remove(path)
		}
	})
}
