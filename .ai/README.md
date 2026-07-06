# publicnext-awsconfig — Repository Engineering Kit

> **Platform Engineering Kit**: `../../.ai/README.md`

| Property | Value |
|---|---|
| Type | Shared Library (not a runnable service) |
| Module | github.com/vishalpsheth/publicnext-awsconfig |
| Version | v1.1.0 |
| Consumers | All Go microservices |

## Package Structure

```
publicnext-awsconfig/
├── config/config.go          ← Generic config loader (AWS SM + .env + env)
├── config/config_test.go     ← Config loading tests
├── infra/
│   ├── mongo.go              ← MongoDB client factory (pool, timeout)
│   ├── redis.go              ← Redis client factory (TLS auto-detect)
│   └── redis_test.go         ← TLS detection tests
├── apperror/errors.go        ← Standardized error codes + WriteError()
├── middleware/
│   ├── maxbytes.go           ← Request body size limiting
│   ├── loadshed.go           ← Adaptive load shedding (goroutine-based)
│   └── tracelog.go           ← Trace-log correlation (OTel → Zap)
├── pagination/               ← Cursor encode/decode
├── featureflags/flags.go     ← MongoDB-backed feature flags (30s poll)
├── fieldselect/filter.go     ← ?fields= response filtering
├── go.mod
└── go.sum
```

## Exported API

### Config Types
- `CoreConfig` — MongoURI, MongoDB, RedisAddr, PostgresDSN, AWSRegion, Environment
- `StreamsConfig` — CoreConfig + ConsumerName, MaxRetries
- `WebConfig` — CoreConfig + AppDomain, AppDownloadLink
- `MediaConfig` — CoreConfig + PrimaryImageCDN, BackupImageCDN, PrimaryVideoCDN, BackupVideoCDN

### Config Loading
- `Load[T](secretName, opts...)` — Generic loader
- Priority: AWS SM (3 retries) → .env fallback → env var overrides
- Validation: only `required:"true"` tagged fields

### Infrastructure
- `infra.NewMongoClient(uri, db, opts...)` — Pooled MongoDB client
- `infra.NewRedisClient(addr, opts...)` — TLS auto-detecting Redis client

### Error Handling
- `apperror.WriteError(w, err)` — Write JSON error response
- Codes: NOT_FOUND, INVALID_INPUT, RATE_LIMITED, USER_BLOCKED, INTERNAL_ERROR, CIRCUIT_OPEN, TIMEOUT, UNAUTHORIZED, FORBIDDEN

### Middleware
- `middleware.MaxBytes(n)` — Body size limit (POST/PUT/PATCH only)
- `middleware.LoadShedding(cfg, reg)` — Goroutine-based with hysteresis
- `middleware.TraceLogCorrelation(logger)` — Injects trace_id/span_id

### Utilities
- `RedactCredentials(uri)` — Safe credential logging
- `pagination.EncodeCursor(score)` / `DecodeCursor(cursor)`
- `featureflags.NewClient(db, logger, interval)` — Feature flag polling
- `fieldselect.Filter(data, fields)` — Response field filtering

## Development

```bash
# Run tests
go test -race ./...

# Consumer services reference via replace:
replace github.com/vishalpsheth/publicnext-awsconfig => ../publicnext-awsconfig
```

## Rules

- Changes affect ALL services — ensure backward compatibility
- Add new functions rather than modifying existing signatures
- Test with at least one consumer service before pushing
