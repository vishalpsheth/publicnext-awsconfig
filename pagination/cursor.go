// Package pagination provides cursor-based pagination utilities for feed APIs.
// Cursor format: base64(JSON{"updated_at": unix_timestamp})
package pagination

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Cursor represents the pagination position using the sort key of the last item.
type Cursor struct {
	UpdatedAt int64 `json:"updated_at"`
}

// Meta contains pagination metadata included in paginated responses.
type Meta struct {
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// EncodeCursor encodes an updated_at timestamp into a cursor string.
func EncodeCursor(updatedAt int64) string {
	b, _ := json.Marshal(Cursor{UpdatedAt: updatedAt})
	return base64.URLEncoding.EncodeToString(b)
}

// DecodeCursor decodes a cursor string into an updated_at timestamp.
// Returns error if the cursor is malformed, not valid base64, or missing updated_at.
func DecodeCursor(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty cursor")
	}

	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor encoding: %w", err)
	}

	var c Cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return 0, fmt.Errorf("invalid cursor format: %w", err)
	}

	if c.UpdatedAt == 0 {
		return 0, fmt.Errorf("cursor missing updated_at")
	}

	return c.UpdatedAt, nil
}

// DefaultLimit is the default page size when limit is not specified.
const DefaultLimit = 20
