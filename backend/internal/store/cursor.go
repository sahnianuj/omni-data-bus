package store

import (
	"encoding/base64"
	"strings"
	"time"
)

// EncodeCursor makes an opaque keyset cursor from the last item's (createdAt, id).
func EncodeCursor(createdAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt.UTC().Format(time.RFC3339Nano) + "|" + id))
}

// DecodeCursor reverses EncodeCursor. An empty cursor means "first page".
func DecodeCursor(c string) (createdAt time.Time, id string, err error) {
	if c == "" {
		return time.Time{}, "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", ErrInvalidCursor
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok || id == "" {
		return time.Time{}, "", ErrInvalidCursor
	}
	createdAt, err = time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, "", ErrInvalidCursor
	}
	return createdAt, id, nil
}

// Before reports whether (at, id) sorts after the cursor position in newest-first order.
func Before(at time.Time, id string, cursorAt time.Time, cursorID string) bool {
	if at.Equal(cursorAt) {
		return id < cursorID
	}
	return at.Before(cursorAt)
}
