package types

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CursorPage is the standard list response for all ANI list endpoints.
// T is the item type.
type CursorPage[T any] struct {
	Items      []T     `json:"items"`
	Total      int64   `json:"total"`
	NextCursor *string `json:"next_cursor"` // nil means last page
}

// ListRequest is the standard pagination request.
type ListRequest struct {
	Limit  int    // max items per page; default 20, max 100
	Cursor string // opaque cursor from previous response
}

// Normalize clamps Limit to [1, 100] and defaults to 20.
func (r *ListRequest) Normalize() {
	if r.Limit <= 0 {
		r.Limit = 20
	}
	if r.Limit > 100 {
		r.Limit = 100
	}
}

// EncodeCursor encodes created_at + id into an opaque base64 cursor string.
func EncodeCursor(createdAt time.Time, id uuid.UUID) string {
	raw := fmt.Sprintf("%s|%s", createdAt.UTC().Format(time.RFC3339Nano), id.String())
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor decodes a cursor string back to (createdAt, id).
// Returns an error if the cursor is malformed; the caller should treat this as a bad request.
func DecodeCursor(cursor string) (time.Time, uuid.UUID, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.UUID{}, fmt.Errorf("invalid cursor encoding: %w", err)
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return time.Time{}, uuid.UUID{}, fmt.Errorf("malformed cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.UUID{}, fmt.Errorf("cursor timestamp: %w", err)
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.UUID{}, fmt.Errorf("cursor id: %w", err)
	}
	return t, id, nil
}
