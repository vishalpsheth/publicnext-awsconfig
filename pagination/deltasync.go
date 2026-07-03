package pagination

import (
	"fmt"
	"math"
	"strconv"
)

// DeltaSyncMeta extends standard pagination metadata with delta sync fields.
// LatestScore is the highest sorted set score in the returned result set,
// which clients use as their next since_score value.
type DeltaSyncMeta struct {
	HasMore     bool    `json:"has_more"`
	NextCursor  string  `json:"next_cursor,omitempty"`
	LatestScore float64 `json:"latest_score,omitempty"`
}

// ParseSinceScore parses the since_score query parameter.
//
// Returns:
//   - (score, true, nil) if the parameter is present and valid
//   - (0, false, nil) if the parameter is absent (empty string)
//   - (0, false, error) if the parameter is present but not a valid finite number
func ParseSinceScore(raw string) (float64, bool, error) {
	if raw == "" {
		return 0, false, nil
	}
	score, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false, fmt.Errorf("since_score must be a valid number")
	}
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0, false, fmt.Errorf("since_score must be a finite number")
	}
	return score, true, nil
}
