package utils

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func SiteAssetName(name string) bool {
	return name == filepath.Base(name) && !strings.ContainsAny(name, "/\\") && (strings.HasPrefix(name, "site-logo-") || strings.HasPrefix(name, "site-background-"))
}

func SaveSiteAsset(dir, name string, body []byte) error {
	if dir == "" {
		dir = "data/site-assets"
	}
	if !SiteAssetName(name) {
		return errors.New("invalid site asset name")
	}
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".asset-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, name))
}

// Keep existing URLs, but separate mutable files from shipped JS/CSS.
func ServeSiteAsset(dir string, w http.ResponseWriter, r *http.Request) bool {
	name := strings.TrimPrefix(r.URL.Path, "/assets/")
	if !SiteAssetName(name) {
		return false
	}
	if dir == "" {
		dir = "data/site-assets"
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		path = filepath.Join("static", "assets", name)
	}
	http.ServeFile(w, r, path)
	return true
}

func PreserveSiteAssets(dir string) error {
	if dir == "" {
		dir = "data/site-assets"
	}
	entries, err := os.ReadDir(filepath.Join("static", "assets"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !SiteAssetName(entry.Name()) {
			continue
		}
		if _, err = os.Stat(filepath.Join(dir, entry.Name())); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		f, err := os.Open(filepath.Join("static", "assets", entry.Name()))
		if err != nil {
			return err
		}
		body, readErr := io.ReadAll(io.LimitReader(f, 9<<20))
		f.Close()
		if readErr != nil {
			return readErr
		}
		if len(body) >= 9<<20 {
			return errors.New("legacy site asset exceeds preservation limit")
		}
		if err = SaveSiteAsset(dir, entry.Name(), body); err != nil {
			return err
		}
	}
	return nil
}
