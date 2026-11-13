# awsconfig

A batteries-included Go library for loading application configuration from AWS Secrets Manager or local files. Purpose-built config structs based on actual usage patterns across your repos.

## Features

- **Purpose-built config structs** - `CoreConfig`, `StreamsConfig`, `WebConfig`, `MediaConfig`
- **Type-safe** using Go 1.18+ generics
- **Automatic environment detection** - local `.env` in dev, AWS Secrets in production
- **Zero boilerplate** - most repos need just 1 line of code
- **Extensible** - easily compose configs or add custom fields
- **Credential redaction** for safe logging

## Installation

```bash
go get github.com/yourorg/awsconfig
```

## Config Types

Based on analysis of your actual repos, the library provides:

### CoreConfig (6 fields - common to ALL repos)
```go
MongoURI, MongoDB
RedisAddr, PostgresDSN
AWSRegion, Environment
```

### StreamsConfig (Core + streaming fields)
```go
CoreConfig
+ ConsumerName, MaxRetries
```
**Used by:** streams, consumer-metadata, consumer-home

### WebConfig (Core + web fields)
```go
CoreConfig
+ AppDomain, AppDownloadLink
```
**Used by:** web/API services

### MediaConfig (Core + CDN fields)
```go
CoreConfig
+ PrimaryImageCDN, BackupImageCDN
+ PrimaryVideoCDN, BackupVideoCDN
```
**Used by:** media handling services

## Quick Start

### Streams Repos
```go
cfg, err := awsconfig.LoadStreams("prod/publicnext/streams-config")
// cfg has: MongoURI, MongoDB, RedisAddr, PostgresDSN, 
//          AWSRegion, Environment, ConsumerName, MaxRetries
```

### Web Repos
```go
cfg, err := awsconfig.LoadWeb("prod/publicnext/web-config")
// cfg has: CoreConfig + AppDomain, AppDownloadLink
```

### Media Repos
```go
cfg, err := awsconfig.LoadMedia("prod/publicnext/media-config")
// cfg has: CoreConfig + CDN URLs
```

### Minimal Repos (only need core 6 fields)
```go
cfg, err := awsconfig.LoadCore("prod/publicnext/minimal-config")
// cfg has: just the 6 core fields
```

## Real Usage Examples

### Your Streams Repo
**Before (200+ lines):**
```go
// Complex environment detection
// AWS SDK setup
// Secret fetching and parsing
// Field assignment
// Validation
// etc...
```

**After (5 lines):**
```go
package config

import "github.com/yourorg/awsconfig"

type Config struct {
    *awsconfig.StreamsConfig
}

func Load() (*Config, error) {
    cfg, err := awsconfig.LoadStreams("prod/publicnext/streams-config")
    if err != nil {
        return nil, err
    }
    return &Config{StreamsConfig: cfg}, nil
}
```

### Your Web Repo
```go
func Load() (*awsconfig.WebConfig, error) {
    return awsconfig.LoadWeb("prod/publicnext/web-config")
}
```

### Repo Needing Multiple Concerns (Streams + Media)
```go
type MyConfig struct {
    awsconfig.StreamsConfig
    PrimaryImageCDN string `json:"PRIMARY_IMG_CDN" env:"PRIMARY_IMG_CDN"`
    BackupImageCDN  string `json:"BACKUP_IMG_CDN" env:"BACKUP_IMG_CDN"`
}

cfg, err := awsconfig.Load[MyConfig]("prod/publicnext/my-config")
```

## Configuration Files

### Local Development (.env file)

**For Streams repos:**
```env
MONGO_URI=mongodb://localhost:27017
MONGO_DB=mydb
CACHE_ADDR=localhost:6379
POSTGRES_DSN=postgres://user:pass@localhost/db
AWS_REGION=ap-south-1
ENVIRONMENT=DEV
CONSUMER_NAME=my-consumer
MAX_RETRIES=3
```

**For Web repos:**
```env
# Core fields (same as above)
MONGO_URI=mongodb://localhost:27017
MONGO_DB=mydb
CACHE_ADDR=localhost:6379
POSTGRES_DSN=postgres://user:pass@localhost/db
AWS_REGION=ap-south-1
ENVIRONMENT=DEV
# Web-specific fields
APP_DOMAIN=http://localhost:3000
APP_DOWNLOAD_LINK=http://localhost:3000/download
```

**For Media repos:**
```env
# Core fields (same as above)
MONGO_URI=mongodb://localhost:27017
...
# Media-specific fields
PRIMARY_IMG_CDN=https://cdn1.example.com/images
BACKUP_IMG_CDN=https://cdn2.example.com/images
PRIMARY_VID_CDN=https://cdn1.example.com/videos
BACKUP_VID_CDN=https://cdn2.example.com/videos
```

### AWS Secrets Manager (JSON format)

**For Streams repos:**
```json
{
  "MONGO_URI": "mongodb://prod-server:27017",
  "MONGO_DB": "proddb",
  "CACHE_ADDR": "redis.prod:6379",
  "POSTGRES_DSN": "postgres://user:pass@prod/db",
  "AWS_REGION": "ap-south-1",
  "ENVIRONMENT": "PROD",
  "CONSUMER_NAME": "prod-consumer",
  "MAX_RETRIES": 5
}
```

**For Web repos:**
```json
{
  "MONGO_URI": "mongodb://prod-server:27017",
  "MONGO_DB": "proddb",
  "CACHE_ADDR": "redis.prod:6379",
  "POSTGRES_DSN": "postgres://user:pass@prod/db",
  "AWS_REGION": "ap-south-1",
  "ENVIRONMENT": "PROD",
  "APP_DOMAIN": "https://publicnext.com",
  "APP_DOWNLOAD_LINK": "https://bit.ly/3Q6wmrW"
}
```

## Advanced Usage

### With Options
```go
cfg, err := awsconfig.LoadStreams("prod/publicnext/streams-config", awsconfig.LoadOptions{
    Verbose:   true,              // Enable logging
    LocalPath: ".env.local",      // Custom local file
    AWSRegion: "us-west-2",       // Override region
    ForceAWS:  true,              // Skip local, use AWS only
})
```

### Custom Validation
```go
type MyStreamsConfig struct {
    *awsconfig.StreamsConfig
}

func (c *MyStreamsConfig) Validate() error {
    if err := c.StreamsConfig.Validate(); err != nil {
        return err
    }
    
    if c.MaxRetries > 100 {
        return fmt.Errorf("MaxRetries too high")
    }
    
    return nil
}

// Load with custom validation
cfg, err := awsconfig.LoadStreams("secret", awsconfig.LoadOptions{
    SkipValidation: true,
})
wrapped := &MyStreamsConfig{StreamsConfig: cfg}
err = wrapped.Validate()
```

### Environment-Specific Secrets
```go
func Load() (*awsconfig.StreamsConfig, error) {
    env := os.Getenv("ENVIRONMENT")
    
    var secretName string
    switch env {
    case "DEV":
        secretName = "dev/publicnext/streams-config"
    case "QA":
        secretName = "qa/publicnext/streams-config"
    default:
        secretName = "prod/publicnext/streams-config"
    }
    
    return awsconfig.LoadStreams(secretName)
}
```

### Completely Custom Config
```go
type MyCustomConfig struct {
    awsconfig.CoreConfig  // Start with core fields
    APIKey       string `json:"API_KEY" env:"API_KEY"`
    WebhookURL   string `json:"WEBHOOK_URL" env:"WEBHOOK_URL"`
    RateLimitRPS int64  `json:"RATE_LIMIT_RPS" env:"RATE_LIMIT_RPS"`
}

cfg, err := awsconfig.Load[MyCustomConfig]("prod/myapp/custom-config")
```

## LoadOptions Reference

```go
type LoadOptions struct {
    LocalPath      string  // Override .env path (default: ".env")
    AWSRegion      string  // Override AWS region (default: from env or ap-south-1)
    ForceAWS       bool    // Skip local, always use AWS
    Verbose        bool    // Enable detailed logging
    SkipValidation bool    // Skip automatic validation
}
```

## Precedence Modes (opt-in per-field resolution)

`LoadOptions.Precedence` selects how overlapping sources are resolved. It is
fully backward compatible: the zero value keeps the historical loader behavior.

```go
const (
    PrecedenceLegacy                 Precedence = iota // zero value — unchanged behavior
    PrecedenceSecretOverEnvOverFile                    // opt-in per-field merge
)
```

### `PrecedenceLegacy` (default)
The historical path, unchanged: the Secrets Manager JSON is unmarshalled as a
whole into your struct, `.env` is used only as a fallback if Secrets Manager is
unreachable, and process-environment overrides are applied last. Existing callers
need no changes.

### `PrecedenceSecretOverEnvOverFile` (opt-in)
Three sources are loaded **independently** — the `.env` file (read into a map,
never mutating the process environment), the process environment, and the
Secrets Manager JSON — and merged **per field** in the order
**secret > environment > file**. Defaults are not injected at load time;
validation then runs through the existing mechanism.

```go
cfg, err := awsconfig.LoadStreams("prod/publicnext/appconfig", awsconfig.LoadOptions{
    Precedence: awsconfig.PrecedenceSecretOverEnvOverFile,
})
```

Field-level semantics:

- **Presence vs. zero value**: a field present in a higher source always wins,
  even when its value is `false`, `0`, or `""`. These are authoritative, never
  treated as "missing".
- **Missing key**: a key absent from the secret falls back to env, then file. A
  key missing from all three is left at its Go zero value (optional) or fails the
  existing required-field validation.
- **JSON `null` vs. present empty string**: for every field kind, including
  `string`, a secret value of JSON `null` is treated as **absent** — the field is
  not set from the secret and resolution falls through to environment, then file,
  then the Go zero value. In particular, a `null` on a `string` field does **not**
  set that field to `""`; it permits fallback. To set a field authoritatively to
  the empty string, send a present empty JSON string (`""`): that is a *present*
  value, so it wins over all lower sources and stops fallback. The same holds for
  the environment and file layers, where a present-but-empty value (`KEY=`) is an
  authoritative empty string, not an absent key.
- **Typed parsing is strict**: tokens are parsed with `strconv`/`time.ParseDuration`
  and any malformed value (`"5abc"` for an int, `"yes"` for a bool, a bare number
  for a `time.Duration`) is a hard startup error naming the field — never silently
  coerced. A `time.Duration` requires a unit (e.g. `"30s"`).
- **Bare JSON numbers**: a bare number is accepted on a string field (`8080` →
  `"8080"`) and on numeric fields; it is rejected on a `time.Duration` field.
- **Lower-priority sources are not parsed eagerly**: for each field the merge
  selects the single highest *present* layer (secret, then environment, then
  file) and decodes **only that layer's** token. A lower-priority layer is never
  parsed when a higher-priority layer supplies the field, so a malformed value in
  a lower source (for example `MAX_RETRIES=notanumber` in the environment or file)
  **cannot** fail the load or override a valid value when the secret supplies that
  field validly — the losing layer's token is read for presence but never decoded.
  The only up-front, whole-value parse is the secret body itself: it must be a
  JSON object (a non-object or nil body is a hard error), but its individual
  per-field values are decoded lazily, only for the fields where the secret wins.
  A malformed value is therefore an error only when it belongs to the layer that
  actually wins that field.

ForceLocal in this mode skips Secrets Manager entirely and resolves
**environment > file**.

AWS error handling: `ResourceNotFoundException` permits fallback to env/file;
access-denied, a malformed or non-object secret body, a nil secret string body
(binary-only secret), wrong field types, and transient failures after bounded
retries all fail startup. (A per-key JSON `null` is distinct: it is treated as
absent and permits fallback, as described above.) Errors and logs never contain
secret values. An invalid `Precedence` value is a hard error and never silently
selects local mode.

### Secret-only fields: `secret:"only"` (v1.4.0, opt-in)

Some values must never come from the process environment or a file, for example
an `ENVIRONMENT` selector that is supposed to prove which deployment you are in.
Tag such a field with `secret:"only"`:

```go
type AppConfig struct {
    Environment string `json:"ENVIRONMENT" env:"ENVIRONMENT" secret:"only"`
    MongoDB     string `json:"MONGO_DB" env:"MONGO_DB"` // untagged: secret > env > file
}
```

The tag is purely additive; structs without it behave exactly as in v1.3.0.

Semantics in `PrecedenceSecretOverEnvOverFile` when a secret fetch is attempted
(i.e. not `ForceLocal`):

- The field resolves from the **secret layer only**. The process environment and
  the `.env` file are never consulted for it, even if they hold a value.
- If the key is **absent** from the secret, is JSON **`null`**, or the secret does
  **not exist** (no secret layer), loading fails with
  `awsconfig: field <KEY> is secret-only but absent from secret`. The error names
  the key only, never a value.
- A **present empty string** (`""`) is a present value and is accepted as empty.
  Whether an empty value is acceptable is then up to `required:"true"` or your
  `Validate()`, exactly as for any other field.
- Parsing is strict and identical to other fields (ints, bools, durations, ...).
- Embedded structs are supported (the tag is honored on fields of embedded types).
- The tag value must be exactly `only`. Any other non-empty value (for example a
  typo such as `secret:"onyl"`), or `secret:"only"` on a field with no `env`/`json`
  key, is a hard configuration error in per-field mode. It is reported even under
  `ForceLocal`, so mistakes cannot hide until deployment. The error names the Go
  field, not the tag value.

Interaction with other modes:

- **`ForceLocal`**: the tag is **ignored**. Resolution is environment > file, no
  Secrets Manager client is built and no AWS call is made. The tag value is still
  validated.
- **`PrecedenceLegacy`**: the tag is **ignored** (and not validated). The legacy
  code path is unchanged, so legacy callers are unaffected.
- **Fetch failures** (access denied, malformed body, transient exhaustion) are
  unchanged: they fail startup with the same errors.

> Why this matters: with plain per-field mode a missing secret is an *empty layer*,
> and a field missing from the secret silently falls back to the environment. A
> `secret:"only"` field is the way to say "this value must come from AWS or the
> process must not start".

#### Profile-style bootstrap example

```go
// Use your own struct: the built-in CoreConfig does not carry the tag.
type ProfileBootstrap struct {
    // ENVIRONMENT must come from the secret itself, never from env/config.env.
    Environment string `json:"ENVIRONMENT" env:"ENVIRONMENT" secret:"only" required:"true"`
    MongoURI    string `json:"MONGO_URI" env:"MONGO_URI" required:"true"`
    MongoDB     string `json:"MONGO_DB" env:"MONGO_DB" required:"true"`
}

cfg, err := awsconfig.Load[ProfileBootstrap](os.Getenv("APPCONFIG_SECRET_ID"), awsconfig.LoadOptions{
    Precedence: awsconfig.PrecedenceSecretOverEnvOverFile,
})
if err != nil {
    log.Fatal(err) // missing/null ENVIRONMENT in the secret, unreadable secret, ...
}
// The library proves the value came from the secret; it cannot know which
// deployment you intend. The CALLER cross-checks it:
if cfg.Environment != expectedEnvironment { // from a deployment-provided selector
    log.Fatalf("ENVIRONMENT mismatch: secret does not match this deployment")
}
```

The cross-check is deliberately the caller's job: the library guarantees the
value is sourced from the secret, the caller decides whether it is the *right*
secret for this deployment.

## Validation

### Default Validation
All string fields with `json` or `env` tags are checked to be non-empty.

### Custom Validation
Implement `Validate() error` method on your config struct.

### Skip Validation
```go
cfg, err := awsconfig.LoadStreams("secret", awsconfig.LoadOptions{
    SkipValidation: true,
})
```

## Safe Logging

Use `RedactCredentials()` to hide sensitive data:

```go
log.Printf("MongoURI: %s", awsconfig.RedactCredentials(cfg.MongoURI))
// Output: MongoURI: mongodb://****:****@prod-server:27017
```

## Migration Guide

### Step 1: Install Library
```bash
go get github.com/yourorg/awsconfig
```

### Step 2: Identify Your Config Type

| Your Repo Type | Use This | Has These Fields |
|----------------|----------|------------------|
| Streams/Consumer | `LoadStreams()` | Core + ConsumerName, MaxRetries |
| Web/API | `LoadWeb()` | Core + AppDomain, AppDownloadLink |
| Media/CDN | `LoadMedia()` | Core + CDN URLs |
| Minimal | `LoadCore()` | Just the 6 core fields |
| Custom | `Load[T]()` | Core + your fields |

### Step 3: Replace Config Loading

**Old code (config/config.go):**
```go
func Load() (*Config, error) {
    // 200+ lines of logic
}
```

**New code:**
```go
func Load() (*Config, error) {
    cfg, err := awsconfig.LoadStreams("prod/publicnext/streams-config")
    if err != nil {
        return nil, err
    }
    return &Config{StreamsConfig: cfg}, nil
}
```

### Step 4: Update AWS Secrets
Ensure your AWS secrets are JSON format with the correct field names.

### Step 5: Verify .env Files
Your local `.env` files should already work if you're using godotenv.

### Step 6: Delete Old Code
- Remove old `config/config.go` logic
- Remove `awsutil/secrets.go`
- Keep any custom helper methods (like `GetRedisOptions`)

## Mapping Your Repos

Based on your repo analysis:

| Repo | Config Type | Command |
|------|-------------|---------|
| streams | StreamsConfig | `LoadStreams()` |
| consumer-metadata | StreamsConfig | `LoadStreams()` |
| consumer-home | StreamsConfig | `LoadStreams()` |
| Web/API repos | WebConfig | `LoadWeb()` |
| Media repos | MediaConfig | `LoadMedia()` |

## AWS Permissions

Your IAM role/user needs:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "secretsmanager:GetSecretValue",
      "Resource": "arn:aws:secretsmanager:*:*:secret:prod/publicnext/*"
    }
  ]
}
```

## FAQ

**Q: What if I need fields from multiple config types?**  
A: Embed one and add fields from the other:
```go
type MyConfig struct {
    awsconfig.StreamsConfig
    PrimaryImageCDN string `json:"PRIMARY_IMG_CDN" env:"PRIMARY_IMG_CDN"`
}
```

**Q: What if I need completely different fields?**  
A: Embed `CoreConfig` and add your custom fields:
```go
type MyConfig struct {
    awsconfig.CoreConfig
    MyField string `json:"MY_FIELD" env:"MY_FIELD"`
}
```

**Q: Can I add new common fields to the library?**  
A: Yes! Add to `CoreConfig` or create a new specialized config type, version bump, update repos.

**Q: Do all my repos need all fields?**  
A: No - unused fields will just be empty strings. Or use `CoreConfig` for minimal repos.

**Q: How do I handle different environments?**  
A: Use different secret names per environment (see example above).

## Requirements

- Go 1.18+ (for generics)
- AWS SDK for Go v2
- github.com/joho/godotenv