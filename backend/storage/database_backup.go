package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func writeDatabaseArchive(ctx context.Context, opts Options, mode, key string, body io.ReadSeeker, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsLocal(filepath.FromSlash(key)) {
		return errors.New("invalid backup key")
	}
	checksum, err := ChecksumSHA256Reader(body)
	if err != nil {
		return err
	}
	if _, err = body.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if mode == "s3" {
		return NewS3Client(opts.S3).PutObjectReader(ctx, key, "application/gzip", size, body, checksum)
	}
	if mode == "webdav" {
		return NewWebDAVClient(opts.WebDAV).PutReader(ctx, key, "application/gzip", size, body)
	}
	if mode != "local" {
		return errors.New("invalid backup storage mode")
	}
	dest := filepath.Join(opts.UploadDir, filepath.FromSlash(key))
	if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(dest), ".backup-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	// Database archives contain account data and must not inherit public image permissions.
	n, copyErr := io.Copy(out, body)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n != size {
		return errors.New("backup size mismatch")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return os.Rename(out.Name(), dest)
}
