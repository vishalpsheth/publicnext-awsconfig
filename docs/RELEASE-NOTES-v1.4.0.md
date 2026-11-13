# awsconfig v1.4.0 (DRAFT, unreleased)

Minor release. Purely additive: no existing behavior changes, no new
dependencies, `go.mod`/`go.sum` unchanged.

## Added

- `secret:"only"` struct tag (per-field mode, `PrecedenceSecretOverEnvOverFile`).
  A tagged field resolves from the Secrets Manager layer only: never from the
  process environment, never from the `.env` file. A key that is absent or JSON
  `null` in the secret, or a secret that does not exist, fails the load with
  `awsconfig: field <KEY> is secret-only but absent from secret`. A present empty
  string is a present value. Parsing is strict; embedded structs are supported.
  - Under `ForceLocal` the tag is ignored (env > file, no AWS client, zero calls).
  - Under `PrecedenceLegacy` the tag is ignored; the legacy path is unchanged.
  - Any `secret` tag value other than `only` (and `secret:"only"` on a field with
    no `env`/`json` key) is a hard configuration error in per-field mode.

- `LoadOptions.RequireSecret bool` (zero value false). In per-field mode without
  `ForceLocal`, a secret that does not exist, or whose body is JSON `null`, is a
  hard error instead of an empty layer.
  - `ForceLocal && RequireSecret` fails before any I/O (contradiction).
  - `PrecedenceLegacy && RequireSecret` fails explicitly before any I/O.
  - This is the only public API addition (one optional struct field; all known
    `LoadOptions{...}` literals in the workspace are keyed).

## Compatibility

- Structs without a `secret` tag follow exactly the v1.3.0 code paths. This is
  enforced by `TestGolden_V1_3_0_Equivalence`, which replays a 384-case matrix
  (legacy ForceLocal, per-field ForceLocal, per-field with fake secrets, across
  CoreConfig/StreamsConfig/WebConfig/MediaConfig, a `required:"true"` type and a
  `Validator` type) against `config/testdata/golden_v1_3_0.json`, generated from
  the unmodified v1.3.0 code before the change.
- The v1.3.0 test suite passes unchanged; only tests were added.
- No struct in the PublicNext workspace uses a `secret:` tag today.
