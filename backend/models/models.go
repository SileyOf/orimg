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

type UploadPolicy struct {
	ID                    uint      `json:"id"`
	Name                  string    `json:"name"`
	Slug                  string    `json:"slug"`
	MaxFileMB             int64     `json:"max_file_mb"`
	MaxBatchCount         int       `json:"max_batch_count"`
	MaxDailyUploads       int64     `json:"max_daily_uploads"`
	MaxDailyBytes         int64     `json:"max_daily_bytes"`
	TotalQuotaBytes       int64     `json:"total_quota_bytes"`
	StorageMode           string    `json:"storage_mode"`
	AllowStorageSelection bool      `json:"allow_storage_selection"`
	RetentionDays         int       `json:"retention_days"`
	AllowedTypes          string    `json:"allowed_types"`
	AllowSVG              bool      `json:"allow_svg"`
	AllowAPI              bool      `json:"allow_api"`
	Active                bool      `json:"active"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
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
	StorageMode  string     `json:"storage_mode"`
	StorageKey   string     `json:"storage_key"`
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

type ImageReplica struct {
	ID             uint       `json:"id"`
	ImageID        uint       `json:"image_id"`
	StorageMode    string     `json:"storage_mode"`
	StorageKey     string     `json:"storage_key"`
	Status         string     `json:"status"`
	ChecksumSHA256 string     `json:"checksum_sha256"`
	Size           int64      `json:"size"`
	Attempts       int        `json:"attempts"`
	LastError      string     `json:"last_error"`
	LastVerifiedAt *time.Time `json:"last_verified_at,omitempty"`
	NextRetryAt    *time.Time `json:"next_retry_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type StorageJob struct {
	ID             uint       `json:"id"`
	Type           string     `json:"type"`
	SourceMode     string     `json:"source_mode"`
	TargetMode     string     `json:"target_mode"`
	Status         string     `json:"status"`
	TotalItems     int64      `json:"total_items"`
	ProcessedItems int64      `json:"processed_items"`
	SuccessItems   int64      `json:"success_items"`
	FailedItems    int64      `json:"failed_items"`
	TotalBytes     int64      `json:"total_bytes"`
	ProcessedBytes int64      `json:"processed_bytes"`
	LastError      string     `json:"last_error"`
	CreatedBy      uint       `json:"created_by"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
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
