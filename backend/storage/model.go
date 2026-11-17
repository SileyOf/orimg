package storage

import (
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"zhuapic/config"
)

// Storage is one place images can live: a local/NAS directory, an
// S3-compatible bucket or a WebDAV collection. Images are stored on one or
// more storages; each placement is an image copy.
type Storage struct {
	ID            uint          `json:"id"`
	Name          string        `json:"name"`
	Provider      string        `json:"provider"`
	Config        StorageConfig `json:"config"`
	Status        string        `json:"status"` // active | paused | draining
	AcceptUploads bool          `json:"accept_uploads"`
	AutoBackup    bool          `json:"auto_backup"`
	ReadPriority  int           `json:"read_priority"`

	Health          string     `json:"health"` // unknown | ok | offline
	HealthError     string     `json:"health_error"`
	HealthCheckedAt *time.Time `json:"health_checked_at"`
	OfflineUntil    *time.Time `json:"offline_until"`

	IsDefault  bool `json:"is_default"`
	HasSecret  bool `json:"has_secret"`
	Configured bool `json:"configured"`
	Locked     bool `json:"location_locked"`

	Ready   int64 `json:"ready"`
	Pending int64 `json:"pending"`
	Failed  int64 `json:"failed"`
	Missing int64 `json:"missing"`
	Bytes   int64 `json:"bytes"`
}

type StorageConfig struct {
	Directory string `json:"directory"`
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key,omitempty"`
	PathStyle bool   `json:"path_style"`
}

const (
	StatusActive   = "active"
	StatusPaused   = "paused"
	StatusDraining = "draining"
)

// Mode is the I/O protocol for the provider.
func (s Storage) Mode() string {
	switch s.Provider {
	case "local":
		return "local"
	case "webdav":
		return "webdav"
	}
	return "s3"
}

// Writable storages receive new copies; paused and draining ones are still
// readable but take no new data.
func (s Storage) Writable() bool { return s.Status == StatusActive }

// LocationKey identifies where a storage writes. Two storages with the same
// key would overwrite and delete each other's objects.
func (s Storage) LocationKey() string {
	switch s.Mode() {
	case "local":
		dir := filepath.Clean(s.Config.Directory)
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		return "local:" + dir
	case "webdav":
		return "webdav:" + strings.ToLower(strings.TrimRight(s.Config.Endpoint, "/")) + "/" + s.Config.Prefix
	}
	return "s3:" + strings.ToLower(strings.TrimRight(s.Config.Endpoint, "/")) + "/" + s.Config.Bucket + "/" + s.Config.Prefix
}

func validProvider(provider string) bool {
	switch provider {
	case "local", "s3", "r2", "minio", "b2", "webdav":
		return true
	}
	return false
}

// Validate normalises user input. Secrets may be empty when an existing
// storage keeps its saved secret.
func (s *Storage) Validate() error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" || len([]rune(s.Name)) > 80 {
		return errors.New("存储名称需要 1–80 个字符")
	}
	if !validProvider(s.Provider) {
		return errors.New("不支持的存储服务")
	}
	if s.ReadPriority <= 0 {
		s.ReadPriority = 100
		if s.Provider == "local" {
			s.ReadPriority = 10
		}
	}
	if s.ReadPriority > 1000 {
		s.ReadPriority = 1000
	}
	c := &s.Config
	switch s.Mode() {
	case "local":
		dir := strings.TrimSpace(c.Directory)
		if dir == "" {
			return errors.New("请填写本地目录")
		}
		clean := filepath.Clean(dir)
		if clean == "/" || clean == "." {
			return errors.New("不能使用根目录或程序目录，请指定专用目录")
		}
		if !filepath.IsAbs(clean) && !filepath.IsLocal(clean) {
			return errors.New("相对路径不能跳出程序目录")
		}
		*c = StorageConfig{Directory: clean}
		return nil
	case "webdav":
		u, err := url.Parse(strings.TrimSpace(c.Endpoint))
		if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("WebDAV 地址必须是有效的 HTTP(S) 目录地址，不得包含凭据、查询或片段")
		}
		if strings.Contains(c.Prefix, "..") || strings.HasPrefix(strings.TrimSpace(c.Prefix), "/") {
			return errors.New("子目录必须是相对路径，不能含 ..")
		}
		*c = StorageConfig{Endpoint: strings.TrimRight(u.String(), "/"), Prefix: cleanObjectPrefix(c.Prefix), AccessKey: strings.TrimSpace(c.AccessKey), SecretKey: c.SecretKey}
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(c.Endpoint))
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("Endpoint 必须是有效的 HTTP(S) 服务地址，不得包含凭据、查询或片段")
	}
	c.Endpoint = strings.TrimRight(u.String(), "/")
	c.Bucket = strings.TrimSpace(c.Bucket)
	if c.Bucket == "" || strings.ContainsAny(c.Bucket, "/\\?#") {
		return errors.New("请填写有效的 Bucket")
	}
	c.Region = strings.TrimSpace(c.Region)
	if c.Region == "" {
		c.Region = "auto"
	}
	c.AccessKey = strings.TrimSpace(c.AccessKey)
	if c.AccessKey == "" {
		return errors.New("请填写 Access Key")
	}
	if strings.Contains(c.Prefix, "..") || strings.HasPrefix(strings.TrimSpace(c.Prefix), "/") {
		return errors.New("对象前缀必须是相对路径，不能含 ..")
	}
	c.Prefix = cleanObjectPrefix(c.Prefix)
	c.Directory = ""
	return nil
}

// Options turns a storage into I/O options on top of the site defaults.
func (s Storage) Options(base Options) Options {
	opts := base
	opts.UploadDir, opts.S3Prefix = "", s.Config.Prefix
	opts.S3, opts.WebDAV = config.S3Config{}, WebDAVConfig{}
	switch s.Mode() {
	case "local":
		opts.UploadDir = s.Config.Directory
	case "webdav":
		opts.WebDAV = WebDAVConfig{URL: s.Config.Endpoint, Username: s.Config.AccessKey, Password: s.Config.SecretKey}
	default:
		opts.S3.Endpoint, opts.S3.Region, opts.S3.Bucket = s.Config.Endpoint, s.Config.Region, s.Config.Bucket
		opts.S3.AccessKey, opts.S3.SecretKey, opts.S3.ForcePathStyle = s.Config.AccessKey, s.Config.SecretKey, s.Config.PathStyle
	}
	return opts
}
