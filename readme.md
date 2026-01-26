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