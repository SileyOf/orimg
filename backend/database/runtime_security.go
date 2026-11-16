package database

import (
	"context"
	"database/sql"
	"errors"

	"zhuapic/config"
)

// All database checks precede key-file creation and initialization writes.
func PrepareRuntimeSecurity(ctx context.Context, db *sql.DB, cfg *config.Config, fresh bool, installationPath string) error {
	if err := cfg.ValidateConnection(); err != nil {
		return err
	}
	if fresh {
		if err := config.ValidateSecret(cfg.JWTSecret); err != nil {
			return err
		}
	} else {
		if cfg.JWTSecret == "" {
			return errors.New("original JWT_SECRET is missing; restore the original key")
		}
		info, err := Inspect(ctx, db)
		if err != nil {
			return err
		}
		if info.Empty || info.ActiveAdmins == 0 {
			return errors.New("legacy key compatibility requires a recognized database with an active administrator")
		}
		if err := VerifySecrets(ctx, db, cfg.JWTSecret); err != nil {
			return err
		}
	}
	return cfg.PrepareSessionKey(installationPath)
}
