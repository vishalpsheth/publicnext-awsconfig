package pagination

import (
	"encoding/base64"
	"errors"
	"testing"
)

func intp(i int) *int { return &i }

func sampleCtx() CursorContext {
	return CursorContext{
		Type:     "SECTION",
		CityID:   NewOptInt(intp(42)),
		ScopeID:  NewOptInt(intp(11)),
		FilterID: NewOptInt(intp(338)),
	}
}

func TestV2_RoundTrip_WithAndWithoutLD(t *testing.T) {
	ctx := sampleCtx()
	ls := Position{UpdatedAt: 1712345678, ID: 20}

	// without ld
	enc, err := EncodeCursorV2(ctx, ls, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := DecodeCursorV2(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Version != CursorVersion2 || got.LastScanned != ls || got.LastDelivered != nil {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if !got.Context.Equal(ctx) {
		t.Fatalf("ctx mismatch: %+v", got.Context)
	}

	// with ld
	ld := Position{UpdatedAt: 1712345678, ID: 20}
	enc2, err := EncodeCursorV2(ctx, ls, &ld)
	if err != nil {
		t.Fatalf("encode2: %v", err)
	}
	got2, err := DecodeCursorV2(enc2)
	if err != nil {
		t.Fatalf("decode2: %v", err)
	}
	if got2.LastDelivered == nil || *got2.LastDelivered != ld {
		t.Fatalf("ld round-trip mismatch: %+v", got2.LastDelivered)
	}
}

func TestV2_InspectCursor_Dispatch(t *testing.T) {
	// v1 cursor inspected as KindV1
	v1 := EncodeCursor(1712345678)
	if kind, err := InspectCursor(v1); err != nil || kind != KindV1 {
		t.Fatalf("v1 dispatch: kind=%v err=%v", kind, err)
	}
	// v2 cursor inspected as KindV2
	v2, _ := EncodeCursorV2(sampleCtx(), Position{UpdatedAt: 1, ID: 1}, nil)
	if kind, err := InspectCursor(v2); err != nil || kind != KindV2 {
		t.Fatalf("v2 dispatch: kind=%v err=%v", kind, err)
	}
}

func TestV2_StructuralValidation_Correction10(t *testing.T) {
	b64 := func(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }
	cases := []struct {
		name    string
		in      string
		wantErr error
	}{
		// unknown field rejected (DisallowUnknownFields)
		{"unknown-field", b64(`{"v":2,"ctx":{"t":"X","c":{"p":false,"v":0},"s":{"p":false,"v":0},"f":{"p":false,"v":0}},"ls":{"u":1,"i":1},"evil":1}`), ErrCursorMalformed},
		// missing ctx entirely
		{"missing-ctx", b64(`{"v":2,"ls":{"u":1,"i":1}}`), ErrCursorIncomplete},
		// missing one ctx sub-object (c present, s/f absent)
		{"missing-ctx-s-f", b64(`{"v":2,"ctx":{"t":"X","c":{"p":true,"v":3}},"ls":{"u":1,"i":1}}`), ErrCursorIncomplete},
		// trailing data after JSON object
		{"trailing-data", b64(`{"v":2,"ctx":{"t":"X","c":{"p":false,"v":0},"s":{"p":false,"v":0},"f":{"p":false,"v":0}},"ls":{"u":1,"i":1}} EXTRA`), ErrCursorMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeCursorV2(tc.in); !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestV2_OversizedPayloadRejected(t *testing.T) {
	big := make([]byte, MaxCursorBytes*3)
	for i := range big {
		big[i] = 'a'
	}
	if _, err := DecodeCursorV2(base64.URLEncoding.EncodeToString(big)); !errors.Is(err, ErrCursorMalformed) {
		t.Fatalf("oversized payload must be rejected as malformed, got %v", err)
	}
}

// A fully-formed v2 cursor with all ctx sub-objects present still round-trips.
func TestV2_FullContextPresent_RoundTrips(t *testing.T) {
	enc, err := EncodeCursorV2(sampleCtx(), Position{UpdatedAt: 10, ID: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCursorV2(enc); err != nil {
		t.Fatalf("well-formed v2 must decode: %v", err)
	}
}

func TestV2_Rejections(t *testing.T) {
	b64 := func(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }
	cases := []struct {
		name    string
		in      string
		wantErr error
	}{
		{"empty", "", ErrCursorMalformed},
		{"not-base64", "@@@not base64@@@", ErrCursorMalformed},
		{"not-json", b64("nope"), ErrCursorMalformed},
		{"unknown-version", b64(`{"v":99,"ctx":{"t":"X"},"ls":{"u":1,"i":1}}`), ErrCursorVersion},
		{"v2-missing-ctx-type", b64(`{"v":2,"ctx":{"t":""},"ls":{"u":1,"i":1}}`), ErrCursorIncomplete},
		{"v2-missing-ls", b64(`{"v":2,"ctx":{"t":"X"}}`), ErrCursorIncomplete},
		{"v2-zero-ls-updatedat", b64(`{"v":2,"ctx":{"t":"X"},"ls":{"u":0,"i":1}}`), ErrCursorIncomplete},
		{"v2-zero-id", b64(`{"v":2,"ctx":{"t":"X"},"ls":{"u":1,"i":0}}`), ErrCursorIncomplete},
		{"v2-negative-id", b64(`{"v":2,"ctx":{"t":"X"},"ls":{"u":1,"i":-5}}`), ErrCursorIncomplete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeCursorV2(tc.in)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("want errors.Is %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestV2_Encode_RejectsBadIDs(t *testing.T) {
	ctx := sampleCtx()
	if _, err := EncodeCursorV2(ctx, Position{UpdatedAt: 1, ID: 0}, nil); !errors.Is(err, ErrCursorID) {
		t.Fatalf("expected ErrCursorID for zero ls id, got %v", err)
	}
	if _, err := EncodeCursorV2(ctx, Position{UpdatedAt: 1, ID: -1}, nil); !errors.Is(err, ErrCursorID) {
		t.Fatalf("expected ErrCursorID for negative ls id, got %v", err)
	}
	bad := Position{UpdatedAt: 1, ID: -1}
	if _, err := EncodeCursorV2(ctx, Position{UpdatedAt: 1, ID: 1}, &bad); !errors.Is(err, ErrCursorID) {
		t.Fatalf("expected ErrCursorID for negative ld id, got %v", err)
	}
}

func TestV2_ContextBinding(t *testing.T) {
	ctx := sampleCtx()
	enc, _ := EncodeCursorV2(ctx, Position{UpdatedAt: 1, ID: 1}, nil)
	dec, err := DecodeCursorV2(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// same context passes
	if err := dec.ValidateContext(ctx); err != nil {
		t.Fatalf("same ctx should pass: %v", err)
	}

	mismatches := map[string]CursorContext{
		"type":                   {Type: "LATEST", CityID: NewOptInt(intp(42)), ScopeID: NewOptInt(intp(11)), FilterID: NewOptInt(intp(338))},
		"city":                   {Type: "SECTION", CityID: NewOptInt(intp(43)), ScopeID: NewOptInt(intp(11)), FilterID: NewOptInt(intp(338))},
		"scope":                  {Type: "SECTION", CityID: NewOptInt(intp(42)), ScopeID: NewOptInt(intp(12)), FilterID: NewOptInt(intp(338))},
		"filter":                 {Type: "SECTION", CityID: NewOptInt(intp(42)), ScopeID: NewOptInt(intp(11)), FilterID: NewOptInt(intp(339))},
		"city-absent-vs-present": {Type: "SECTION", CityID: NewOptInt(nil), ScopeID: NewOptInt(intp(11)), FilterID: NewOptInt(intp(338))},
	}
	for name, m := range mismatches {
		t.Run(name, func(t *testing.T) {
			if err := dec.ValidateContext(m); !errors.Is(err, ErrCursorCtxMismatch) {
				t.Fatalf("expected ctx mismatch for %s, got %v", name, err)
			}
		})
	}
}

// Absent vs zero must not be equivalent.
func TestV2_OptInt_AbsentNotZero(t *testing.T) {
	absent := NewOptInt(nil)
	zero := NewOptInt(intp(0))
	if absent.Equal(zero) {
		t.Fatalf("absent OptInt must not equal zero-valued OptInt")
	}
}
