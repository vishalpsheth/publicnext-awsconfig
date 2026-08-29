package pagination

import (
	"encoding/base64"
	"testing"
)

// v1 characterization tests (T0.3). These pin the EXISTING v1 wire format and
// behavior so the additive v2 work cannot silently regress v1. They assert
// current behavior only; they do not change it.

func TestV1_RoundTrip(t *testing.T) {
	cases := []int64{1, 2, 100, 1_712_345_678, 9_223_372_036_854_775_807}
	for _, u := range cases {
		enc := EncodeCursor(u)
		got, err := DecodeCursor(enc)
		if err != nil {
			t.Fatalf("DecodeCursor(%q) err=%v", enc, err)
		}
		if got != u {
			t.Fatalf("round-trip mismatch: encoded %d decoded %d", u, got)
		}
	}
}

func TestV1_Rejections(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"not-base64", "!!!not-base64!!!"},
		{"not-json", base64.URLEncoding.EncodeToString([]byte("not json"))},
		{"missing-updated_at", base64.URLEncoding.EncodeToString([]byte(`{"foo":1}`))},
		{"zero-updated_at", EncodeCursor(0)}, // v1 rejects updated_at==0
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeCursor(tc.in); err == nil {
				t.Fatalf("expected error for %q, got nil", tc.in)
			}
		})
	}
}

// TestV1_WireFormatStable pins the exact base64url(JSON{"updated_at":N}) shape so
// a format change is caught.
func TestV1_WireFormatStable(t *testing.T) {
	enc := EncodeCursor(1712345678)
	raw, err := base64.URLEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("v1 cursor not base64url: %v", err)
	}
	want := `{"updated_at":1712345678}`
	if string(raw) != want {
		t.Fatalf("v1 wire format changed: got %q want %q", string(raw), want)
	}
}
