// Package fieldselect provides field filtering for REST API responses.
// Clients pass ?fields=id,title,media to request only specific top-level fields.
// The "id" field is always included regardless of the requested field set.
package fieldselect

import "strings"

// Parse parses a comma-separated fields query parameter into a set of allowed field names.
// Returns nil if the input is empty (meaning "return all fields" — no filtering).
// The "id" field is always included in the returned set.
func Parse(raw string) map[string]struct{} {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	fields := make(map[string]struct{}, len(parts)+1)
	fields["id"] = struct{}{} // always include id
	for _, p := range parts {
		f := strings.TrimSpace(p)
		if f != "" {
			fields[f] = struct{}{}
		}
	}
	return fields
}

// Apply filters a data map to include only keys present in the allowed set.
// If allowed is nil, the original data is returned unmodified (no filtering).
// The "id" key is always preserved if present in the original data.
func Apply(data map[string]interface{}, allowed map[string]struct{}) map[string]interface{} {
	if allowed == nil {
		return data
	}
	result := make(map[string]interface{}, len(allowed))
	for key := range allowed {
		if v, ok := data[key]; ok {
			result[key] = v
		}
	}
	// Ensure id is always present even if not in allowed set
	if v, ok := data["id"]; ok {
		result["id"] = v
	}
	return result
}

// ApplySlice applies field selection to a slice of data maps.
// If allowed is nil, the original slice is returned unmodified.
func ApplySlice(data []map[string]interface{}, allowed map[string]struct{}) []map[string]interface{} {
	if allowed == nil {
		return data
	}
	result := make([]map[string]interface{}, len(data))
	for i, item := range data {
		result[i] = Apply(item, allowed)
	}
	return result
}
