package handlers

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type imageCursor struct {
	CreatedAt time.Time
	ID        uint
}

func encodeImageCursor(createdAt time.Time, id uint) string {
	raw := fmt.Sprintf("%d:%d", createdAt.UnixMicro(), id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeImageCursor(raw string) (*imageCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(decoded), ":")
	if len(parts) != 2 {
		return nil, errors.New("invalid cursor")
	}
	micros, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || micros <= 0 {
		return nil, errors.New("invalid cursor timestamp")
	}
	id, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || id == 0 {
		return nil, errors.New("invalid cursor id")
	}
	return &imageCursor{CreatedAt: time.UnixMicro(micros), ID: uint(id)}, nil
}
