# Proposal: opt-in per-field config precedence (secret > env > file)

**Status: PROPOSAL ONLY — no code change, no release in this task.** Prepared
for publicnext-user-profile's QA requirement. Must preserve existing behavior
for all current callers.

## Problem

`Load[T]` currently resolves config as:
1. AWS Secrets Manager (whole JSON) **or** `.env` (only if SM fails) — mutually exclusive.
2. `populateFromEnv` applies env vars **last = highest priority** (env overrides the secret).

A consumer (Profile) now requires per-field **secret > environment > file >
default**, where a higher layer is never overwritten by a lower one, and the
file is an **independent lowest layer** (not just an SM-failure fallback).

The two orderings are incompatible, and `LoadOptions` has no precedence control.

## Constraints

- **Backward compatible**: existing services (auth-channels, user-transactions,
  consumer-metadata, consumer-home, …) must behave EXACTLY as today unless they
  opt in. Default precedence stays `env > secret`, file-on-SM-failure.
- Do not duplicate SM fetch in services.
- Distinguish missing vs valid `false`/`0`; fail on malformed typed values.
- Differentiate AWS error classes.

## Proposed change (smallest)

1. Add an enum + field to `LoadOptions`:
   ```go
   type Precedence int
   const (
       PrecedenceLegacy Precedence = iota // default: current behavior (env > secret; file on SM failure)
       PrecedenceSecretOverEnvOverFile    // opt-in: per-field secret > env > file > default
   )
   type LoadOptions struct {
       // ... existing fields unchanged ...
       Precedence Precedence // zero value = PrecedenceLegacy (unchanged behavior)
   }
   ```
2. When `Precedence == PrecedenceSecretOverEnvOverFile`, `Load[T]` builds the
   result by merging three independently-loaded layers **per field** (via the
   existing `json`/`env` struct tags), in order:
   - file (`.env` → a map) — lowest,
   - process environment — middle,
   - Secrets Manager JSON — highest,
   then applies struct defaults and runs validation last.
   A field is "present" in a layer only if the key exists in that layer; an
   absent key does NOT clobber a lower layer, and an explicit `false`/`0`/`""`
   that IS present is honored (distinguish presence from zero-value). A
   malformed typed value (e.g. non-integer for an int field) is a hard error.
3. Differentiate AWS errors: return typed sentinels (or classified error) for
   `ResourceNotFoundException`, `AccessDenied`, timeout/context-deadline, and
   JSON-unmarshal failure, so callers can choose fail-closed vs fall back.
   Legacy precedence keeps today's "any error → file fallback" to stay
   backward compatible.

## Non-goals
- No change to secret-name selection (caller passes `secretName`).
- No change to region/credential resolution.
- No new required fields.

## Rollout
1. Land behind the opt-in flag (default legacy). Unit-test both precedences.
2. Tag a minor version (e.g. v1.3.0 — additive, backward compatible).
3. Profile opts in (`Precedence: PrecedenceSecretOverEnvOverFile`) and adds the
   precedence tests. No other service changes.

## Test matrix (to accompany the implementation)
- secret+env+file all set, differ → secret wins.
- secret field missing, env+file set → env wins.
- secret+env missing, file set → file wins.
- `false`/`0`/`""` present in a higher layer → retained (not treated as missing).
- malformed typed value → explicit error.
- required missing → validation error.
- each AWS error class → correct classification; legacy path unchanged.
