package handlers

import (
	"testing"
	"time"
)

func TestImageCursorRoundTrip(t *testing.T) {
	wantTime := time.Date(2026, 8, 27, 12, 34, 56, 123000000, time.Local)
	raw := encodeImageCursor(wantTime, 42)
	got, err := decodeImageCursor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 42 || !got.CreatedAt.Equal(wantTime) {
		t.Fatalf("cursor = %#v, want id=42 time=%s", got, wantTime)
	}
}

func TestImageCursorRejectsInvalidValue(t *testing.T) {
	if _, err := decodeImageCursor("not-a-cursor"); err == nil {
		t.Fatal("expected invalid cursor error")
	}
}
