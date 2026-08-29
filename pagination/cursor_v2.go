// Package pagination — cursor v2 (additive).
//
// v2 is an ADDITIVE codec. The v1 EncodeCursor/DecodeCursor functions in cursor.go
// are unchanged and remain valid for existing callers (video-reels, related-feeds,
// user-transactions). v2 introduces a versioned, query-context-bound, composite
// (updated_at, id) cursor used by Consumer Home for tie-safe, Mongo-backed
// continuation.
//
// Wire format: base64url( JSON{ v, ctx{t,c,s,f}, ls{u,i}, ld{u,i}? } )
// Total order everywhere: (updated_at DESC, id DESC).
package pagination

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// CursorVersion identifies the cursor schema. v1 has no "v" field; v2 sets v=2.
const CursorVersion2 = 2

// Sentinel errors so callers (resolvers) can map to stable GraphQL errors without
// leaking internal detail.
var (
	ErrCursorMalformed   = errors.New("invalid cursor")
	ErrCursorVersion     = errors.New("invalid cursor")
	ErrCursorIncomplete  = errors.New("invalid cursor")
	ErrCursorID          = errors.New("invalid cursor")
	ErrCursorCtxMismatch = errors.New("cursor does not match this query")
)

// OptInt is a normalized optional integer argument. Absence (p=false) is NOT the
// same as a zero value; this avoids treating an absent argument as cityId=0 etc.
type OptInt struct {
	Present bool `json:"p"`
	Value   int  `json:"v"`
}

// NewOptInt builds an OptInt from a *int (nil => absent).
func NewOptInt(p *int) OptInt {
	if p == nil {
		return OptInt{Present: false}
	}
	return OptInt{Present: true, Value: *p}
}

// Equal reports whether two OptInt are equivalent (same presence and, if present,
// same value).
func (o OptInt) Equal(other OptInt) bool {
	if o.Present != other.Present {
		return false
	}
	if !o.Present {
		return true
	}
	return o.Value == other.Value
}

// CursorContext is the normalized query context a v2 cursor is bound to. A cursor
// minted for one query must be rejected when replayed against a different query.
// It intentionally contains NO Redis/Mongo phase and NO index generation.
type CursorContext struct {
	Type     string `json:"t"` // canonical feed type string, e.g. "LATEST", "SECTION"
	CityID   OptInt `json:"c"`
	ScopeID  OptInt `json:"s"`
	FilterID OptInt `json:"f"`
}

// Equal reports whether two contexts are identical for binding purposes.
func (c CursorContext) Equal(other CursorContext) bool {
	return c.Type == other.Type &&
		c.CityID.Equal(other.CityID) &&
		c.ScopeID.Equal(other.ScopeID) &&
		c.FilterID.Equal(other.FilterID)
}

// Position is a composite (updated_at, id) coordinate in the total order.
type Position struct {
	UpdatedAt int64 `json:"u"`
	ID        int64 `json:"i"`
}

// CursorV2 is the decoded v2 cursor.
//   - LastScanned (ls) is REQUIRED and drives continuation. It may reference a
//     stale/dropped candidate; continuation resumes strictly after it.
//   - LastDelivered (ld) is OPTIONAL and references only a delivered article; it
//     is omitted on a zero-delivery page.
type CursorV2 struct {
	Version       int           `json:"v"`
	Context       CursorContext `json:"ctx"`
	LastScanned   Position      `json:"ls"`
	LastDelivered *Position     `json:"ld,omitempty"`
}

// validID enforces the article-ID contract (application-enforced; OC-7 closed):
// positive, within int64, and 20-digit formattable. MaxInt64 is 19 digits, so any
// positive int64 fits width 20; we reject <=0 and overflow-adjacent inputs.
func validID(id int64) bool {
	return id > 0 && id <= math.MaxInt64
}

// EncodeCursorV2 encodes a v2 cursor. lastDelivered may be nil (zero-delivery page).
// Returns an error if any position ID is invalid.
func EncodeCursorV2(ctx CursorContext, lastScanned Position, lastDelivered *Position) (string, error) {
	if !validID(lastScanned.ID) {
		return "", fmt.Errorf("%w: lastScanned id out of range", ErrCursorID)
	}
	if lastDelivered != nil && !validID(lastDelivered.ID) {
		return "", fmt.Errorf("%w: lastDelivered id out of range", ErrCursorID)
	}
	c := CursorV2{
		Version:       CursorVersion2,
		Context:       ctx,
		LastScanned:   lastScanned,
		LastDelivered: lastDelivered,
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("%w: marshal", ErrCursorMalformed)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// MaxCursorBytes bounds the decoded payload size before parsing (defense against
// oversized/abusive cursors). A valid v2 cursor is well under this.
const MaxCursorBytes = 512

// DecodeKind reports which cursor version a raw string is.
type DecodeKind int

const (
	KindInvalid DecodeKind = iota
	KindV1
	KindV2
)

// rawCursor is a single-parse decode target that captures BOTH the v1 marker and
// the full v2 shape, so InspectCursor performs exactly ONE json parse. Presence
// pointers let us distinguish "field absent" from "field present with zero value"
// (Correction 10): a missing ctx sub-object is rejected, not treated as absent.
type rawCursor struct {
	V         *int        `json:"v"`
	UpdatedAt *int64      `json:"updated_at"` // v1 shape marker
	Ctx       *rawContext `json:"ctx"`
	LS        *Position   `json:"ls"`
	LD        *Position   `json:"ld"`
}

type rawContext struct {
	Type *string `json:"t"`
	C    *OptInt `json:"c"`
	S    *OptInt `json:"s"`
	F    *OptInt `json:"f"`
}

// decodeOnce base64-decodes (size-bounded) and JSON-parses exactly once with
// unknown-field rejection.
func decodeOnce(s string) (*rawCursor, DecodeKind, error) {
	if s == "" {
		return nil, KindInvalid, fmt.Errorf("%w: empty", ErrCursorMalformed)
	}
	// Bound the ENCODED length first (cheap) to avoid decoding huge inputs.
	if len(s) > MaxCursorBytes*2 {
		return nil, KindInvalid, fmt.Errorf("%w: too large", ErrCursorMalformed)
	}
	raw, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return nil, KindInvalid, fmt.Errorf("%w: base64", ErrCursorMalformed)
	}
	if len(raw) > MaxCursorBytes {
		return nil, KindInvalid, fmt.Errorf("%w: payload too large", ErrCursorMalformed)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var rc rawCursor
	if err := dec.Decode(&rc); err != nil {
		return nil, KindInvalid, fmt.Errorf("%w: json", ErrCursorMalformed)
	}
	// Reject trailing data after the JSON object.
	if dec.More() {
		return nil, KindInvalid, fmt.Errorf("%w: trailing data", ErrCursorMalformed)
	}
	switch {
	case rc.V == nil && rc.UpdatedAt != nil:
		return &rc, KindV1, nil
	case rc.V != nil && *rc.V == CursorVersion2:
		return &rc, KindV2, nil
	default:
		return &rc, KindInvalid, fmt.Errorf("%w: unknown version", ErrCursorVersion)
	}
}

// InspectCursor decodes ONCE and reports the cursor kind. Callers that only need
// dispatch (v1 vs v2) use this; DecodeCursorV2 reuses the same single parse.
func InspectCursor(s string) (DecodeKind, error) {
	_, kind, err := decodeOnce(s)
	return kind, err
}

// DecodeCursorV2 decodes (single parse) and structurally validates a v2 cursor:
// version, structurally-present context with all sub-objects, required ls, valid
// IDs. It does NOT check context binding — call ValidateContext for that.
func DecodeCursorV2(s string) (*CursorV2, error) {
	rc, kind, err := decodeOnce(s)
	if err != nil {
		return nil, err
	}
	if kind != KindV2 {
		return nil, fmt.Errorf("%w: not a v2 cursor", ErrCursorVersion)
	}
	// Context must be structurally present with all sub-objects (Correction 10):
	// a missing ctx / missing c|s|f is rejected, not silently treated as absent.
	if rc.Ctx == nil {
		return nil, fmt.Errorf("%w: missing ctx", ErrCursorIncomplete)
	}
	if rc.Ctx.Type == nil || *rc.Ctx.Type == "" {
		return nil, fmt.Errorf("%w: missing ctx.t", ErrCursorIncomplete)
	}
	if rc.Ctx.C == nil || rc.Ctx.S == nil || rc.Ctx.F == nil {
		return nil, fmt.Errorf("%w: missing ctx.c/s/f", ErrCursorIncomplete)
	}
	if rc.LS == nil {
		return nil, fmt.Errorf("%w: missing ls", ErrCursorIncomplete)
	}
	if rc.LS.UpdatedAt <= 0 || !validID(rc.LS.ID) {
		return nil, fmt.Errorf("%w: ls", ErrCursorIncomplete)
	}
	if rc.LD != nil && (rc.LD.UpdatedAt <= 0 || !validID(rc.LD.ID)) {
		return nil, fmt.Errorf("%w: ld", ErrCursorID)
	}

	c := &CursorV2{
		Version: CursorVersion2,
		Context: CursorContext{
			Type:     *rc.Ctx.Type,
			CityID:   *rc.Ctx.C,
			ScopeID:  *rc.Ctx.S,
			FilterID: *rc.Ctx.F,
		},
		LastScanned: *rc.LS,
	}
	if rc.LD != nil {
		ld := *rc.LD
		c.LastDelivered = &ld
	}
	return c, nil
}

// ValidateContext rejects a decoded v2 cursor whose bound context does not exactly
// match the current normalized request context (query-context replay protection).
func (c *CursorV2) ValidateContext(current CursorContext) error {
	if !c.Context.Equal(current) {
		return ErrCursorCtxMismatch
	}
	return nil
}
