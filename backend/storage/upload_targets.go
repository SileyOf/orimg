package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"zhuapic/utils"
)

func TargetSelection(id uint) string { return "target:" + strconv.FormatUint(uint64(id), 10) }

func ParseTargetSelection(value string) (uint, error) {
	if !strings.HasPrefix(value, "target:") {
		return 0, errors.New("无效的存储选择")
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(value, "target:"), 10, 64)
	if err != nil || id == 0 {
		return 0, errors.New("无效的存储选择")
	}
	return uint(id), nil
}

// A group's assigned default is usable even when not offered as an optional destination.
func CanSelectTarget(t *Target, defaultID uint, policy string, loggedIn, admin, allowSelection bool) bool {
	if !t.Enabled || !t.Configured {
		return false
	}
	isDefault := t.ID == defaultID
	if policy != "" && policy != "default" {
		isDefault = policy == TargetSelection(t.ID) || policy == t.LegacyMode
	}
	return isDefault || (allowSelection && (admin || t.UploadAccess == "public" || (loggedIn && t.UploadAccess == "members")))
}

func (c *Coordinator) UploadTargets(ctx context.Context) ([]Target, error) {
	rows, err := c.db.QueryContext(ctx, "SELECT id,name,provider,COALESCE(legacy_mode,''),upload_access,config_json FROM storage_targets WHERE enabled=1 ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Target{}
	for rows.Next() {
		var t Target
		var raw string
		var config TargetConfig
		if err := rows.Scan(&t.ID, &t.Name, &t.Provider, &t.LegacyMode, &t.UploadAccess, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			continue
		}
		t.Enabled = true
		t.Configured = t.Mode() == "local" || (config.Endpoint != "" && config.Bucket != "" && config.AccessKey != "" && config.SecretKey != "")
		result = append(result, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	legacyS3 := c.manager.S3Configured(c.options())
	for i := range result {
		if result[i].LegacyMode == "s3" {
			result[i].Configured = legacyS3
		}
	}
	return result, nil
}

func (c *Coordinator) saveLegacyTarget(ctx context.Context, t, previous *Target) error {
	changes := map[string]string{}
	if t.LegacyMode == "local" {
		if t.Config.Directory != previous.Config.Directory {
			candidate := *t
			candidate.LegacyMode = ""
			if err := ValidateTarget(&candidate); err != nil {
				return err
			}
			changes["local_upload_dir"] = candidate.Config.Directory
		}
	} else {
		if t.Config != previous.Config {
			candidate := *t
			candidate.LegacyMode = ""
			if err := ValidateTarget(&candidate); err != nil {
				return err
			}
			old := previous.Config
			for key, pair := range map[string][2]string{
				"s3_endpoint": {old.Endpoint, candidate.Config.Endpoint}, "s3_region": {old.Region, candidate.Config.Region},
				"s3_bucket": {old.Bucket, candidate.Config.Bucket}, "s3_key_prefix": {old.Prefix, candidate.Config.Prefix},
				"s3_access_key": {old.AccessKey, candidate.Config.AccessKey}, "s3_force_path_style": {strconv.FormatBool(old.PathStyle), strconv.FormatBool(candidate.Config.PathStyle)},
			} {
				if pair[0] != pair[1] {
					changes[key] = pair[1]
				}
			}
			if candidate.Config.SecretKey != old.SecretKey {
				secret, err := utils.EncryptSecret(candidate.Config.SecretKey, c.cfg.JWTSecret)
				if err != nil {
					return err
				}
				changes["s3_secret_key"] = secret
			}
		}
	}
	if err := c.CheckLegacyLocationChanges(ctx, changes); err != nil {
		return err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range changes {
		if _, err := tx.ExecContext(ctx, "INSERT INTO site_settings (`key`,value) VALUES (?,?) ON DUPLICATE KEY UPDATE value=VALUES(value)", key, value); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE storage_targets SET name=?,enabled=?,auto_backup=?,upload_access=? WHERE id=?", t.Name, t.Enabled, t.AutoBackup, t.UploadAccess, t.ID); err != nil {
		return err
	}
	return tx.Commit()
}
