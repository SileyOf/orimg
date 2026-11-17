package models

import "time"

type User struct {
	ID            uint      `json:"id"`
	Username      string    `json:"username"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"email_verified"`
	Password      string    `json:"-"`
	Role          string    `json:"role"`
	GroupID       uint      `json:"group_id"`
	GroupName     string    `json:"group_name"`
	AvatarURL     string    `json:"avatar_url"`
	AuthVersion   uint64    `json:"-"`
	QuotaBytes    *int64    `json:"quota_bytes"`
	Active        bool      `json:"active"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// UploadPolicy is the upload part of a user group: limits, quota and which
// storages its members may upload to.
type UploadPolicy struct {
	ID               uint      `json:"id"` // same as the group ID
	Name             string    `json:"name"`
	Slug             string    `json:"slug"`
	MaxFileMB        int64     `json:"max_file_mb"`
	MaxBatchCount    int       `json:"max_batch_count"`
	MaxDailyUploads  int64     `json:"max_daily_uploads"`
	MaxDailyBytes    int64     `json:"max_daily_bytes"`
	TotalQuotaBytes  int64     `json:"total_quota_bytes"`
	DefaultStorageID uint      `json:"default_storage_id"` // 0 follows the site default
	StorageIDs       []uint    `json:"storage_ids"`        // storages members may choose
	RetentionDays    int       `json:"retention_days"`
	AllowedTypes     string    `json:"allowed_types"`
	AllowSVG         bool      `json:"allow_svg"`
	AllowAPI         bool      `json:"allow_api"`
	Active           bool      `json:"active"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type UserGroup struct {
	ID          uint         `json:"id"`
	Name        string       `json:"name"`
	Slug        string       `json:"slug"`
	PolicyID    uint         `json:"policy_id"`
	IsSystem    bool         `json:"is_system"`
	MemberCount int64        `json:"member_count"`
	Policy      UploadPolicy `json:"policy"`
}

type Image struct {
	ID           uint       `json:"id"`
	PublicName   string     `json:"public_name"`
	OriginalName string     `json:"original_name"`
	MimeType     string     `json:"mime_type"`
	Ext          string     `json:"ext"`
	Size         int64      `json:"size"`
	Width        int        `json:"width"`
	Height       int        `json:"height"`
	StorageKey   string     `json:"-"` // object key written during upload
	CopyIDs      string     `json:"-"` // storages holding a ready copy, comma separated
	UserID       uint       `json:"user_id"`
	Username     string     `json:"username"`
	UploadIP     string     `json:"upload_ip"`
	ThumbSmall   string     `json:"thumb_small"`
	ThumbMedium  string     `json:"thumb_medium"`
	ThumbLarge   string     `json:"thumb_large"`
	IsAnimated   bool       `json:"is_animated"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type APIToken struct {
	ID          uint       `json:"id"`
	UserID      uint       `json:"user_id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type AuditLog struct {
	ID          uint      `json:"id"`
	ActorUserID uint      `json:"actor_user_id"`
	ActorLabel  string    `json:"actor_label"`
	Action      string    `json:"action"`
	TargetType  string    `json:"target_type"`
	TargetID    string    `json:"target_id"`
	IP          string    `json:"ip"`
	UserAgent   string    `json:"user_agent"`
	Details     string    `json:"details"`
	CreatedAt   time.Time `json:"created_at"`
}

type SiteSetting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
