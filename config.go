package publicnextawsconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	secretsmanager "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/joho/godotenv"
)

// CoreConfig contains fields that are truly common across ALL repos
type CoreConfig struct {
	MongoURI    string `json:"MONGO_URI" env:"MONGO_URI"`
	MongoDB     string `json:"MONGO_DB" env:"MONGO_DB"`
	RedisAddr   string `json:"CACHE_ADDR" env:"CACHE_ADDR"`
	PostgresDSN string `json:"POSTGRES_DSN" env:"POSTGRES_DSN"`
	AWSRegion   string `json:"AWS_REGION" env:"AWS_REGION"`
	Environment string `json:"ENVIRONMENT" env:"ENVIRONMENT"`
}

// StreamsConfig is for streaming/consumer services
type StreamsConfig struct {
	CoreConfig
	ConsumerName string `json:"CONSUMER_NAME" env:"CONSUMER_NAME"`
	MaxRetries   int64  `json:"MAX_RETRIES" env:"MAX_RETRIES"`
}

// WebConfig is for web/API services
type WebConfig struct {
	CoreConfig
	AppDomain       string `json:"APP_DOMAIN" env:"APP_DOMAIN"`
	AppDownloadLink string `json:"APP_DOWNLOAD_LINK" env:"APP_DOWNLOAD_LINK"`
}

// MediaConfig is for services handling media/CDN
type MediaConfig struct {
	CoreConfig
	PrimaryImageCDN string `json:"PRIMARY_IMG_CDN" env:"PRIMARY_IMG_CDN"`
	BackupImageCDN  string `json:"BACKUP_IMG_CDN" env:"BACKUP_IMG_CDN"`
	PrimaryVideoCDN string `json:"PRIMARY_VID_CDN" env:"PRIMARY_VID_CDN"`
	BackupVideoCDN  string `json:"BACKUP_VID_CDN" env:"BACKUP_VID_CDN"`
}

// Validator is an optional interface for custom validation
type Validator interface {
	Validate() error
}

// LoadOptions contains optional configuration for the loader
type LoadOptions struct {
	// LocalPath overrides the default .env path
	LocalPath string

	// AWSRegion overrides the default region resolution
	AWSRegion string

	// ForceAWS forces AWS Secrets Manager even if local file exists
	ForceAWS bool

	// Verbose enables detailed logging
	Verbose bool

	// SkipValidation skips the automatic validation of required fields
	SkipValidation bool
}

// LoadCore is a convenience function to load CoreConfig
func LoadCore(secretName string, opts ...LoadOptions) (*CoreConfig, error) {
	return Load[CoreConfig](secretName, opts...)
}

// LoadStreams is a convenience function to load StreamsConfig
func LoadStreams(secretName string, opts ...LoadOptions) (*StreamsConfig, error) {
	return Load[StreamsConfig](secretName, opts...)
}

// LoadWeb is a convenience function to load WebConfig
func LoadWeb(secretName string, opts ...LoadOptions) (*WebConfig, error) {
	return Load[WebConfig](secretName, opts...)
}

// LoadMedia is a convenience function to load MediaConfig
func LoadMedia(secretName string, opts ...LoadOptions) (*MediaConfig, error) {
	return Load[MediaConfig](secretName, opts...)
}

// Load loads configuration into the provided struct type using generics
func Load[T any](secretName string, opts ...LoadOptions) (*T, error) {
	var cfg T
	var options LoadOptions

	if len(opts) > 0 {
		options = opts[0]
	}

	// Set defaults
	if options.LocalPath == "" {
		options.LocalPath = ".env"
	}
	if options.AWSRegion == "" {
		options.AWSRegion = resolveRegion()
	}

	// Determine if we should use local or AWS
	useLocal := false
	if !options.ForceAWS && options.LocalPath != "" {
		if _, err := os.Stat(options.LocalPath); err == nil {
			useLocal = true
			if options.Verbose {
				fmt.Printf("📂 Local config file found: %s\n", options.LocalPath)
			}
		}
	}

	var err error
	if useLocal {
		err = loadFromLocal(&cfg, options.LocalPath, options.Verbose)
	} else {
		err = loadFromAWS(&cfg, secretName, options.AWSRegion, options.Verbose)
	}

	if err != nil {
		return nil, err
	}

	// Run validation if not skipped
	if !options.SkipValidation {
		if err := validateConfig(&cfg); err != nil {
			return nil, fmt.Errorf("validation failed: %w", err)
		}
	}

	return &cfg, nil
}

// validateConfig validates the configuration
func validateConfig(cfg interface{}) error {
	// First check if config implements Validator interface
	if validator, ok := cfg.(Validator); ok {
		return validator.Validate()
	}

	// Default validation: check all string fields are non-empty
	return validateRequiredFields(cfg)
}

// validateRequiredFields uses reflection to check all string fields are populated
func validateRequiredFields(cfg interface{}) error {
	v := reflect.ValueOf(cfg)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	t := v.Type()
	var missing []string

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		// Skip embedded structs (handle them recursively)
		if fieldType.Anonymous {
			if err := validateRequiredFields(field.Addr().Interface()); err != nil {
				return err
			}
			continue
		}

		// Check if field has a json or env tag
		jsonTag := fieldType.Tag.Get("json")
		envTag := fieldType.Tag.Get("env")

		if jsonTag == "" && envTag == "" {
			continue // Skip fields without tags
		}

		// Get field name for error message
		fieldName := fieldType.Name
		if jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			fieldName = parts[0]
		} else if envTag != "" {
			fieldName = envTag
		}

		// Check if string field is empty
		if field.Kind() == reflect.String && field.String() == "" {
			missing = append(missing, fieldName)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}

	return nil
}

// loadFromLocal loads config from a local file (.env format)
func loadFromLocal[T any](cfg *T, path string, verbose bool) error {
	if verbose {
		fmt.Printf("📂 Loading from local file: %s\n", path)
	}

	// Load .env file
	if err := godotenv.Load(path); err != nil {
		return fmt.Errorf("failed to load local config file %s: %w", path, err)
	}

	// Use reflection to populate fields from environment variables
	return populateFromEnv(cfg)
}

// loadFromAWS loads config from AWS Secrets Manager
func loadFromAWS[T any](cfg *T, secretName, region string, verbose bool) error {
	if verbose {
		fmt.Printf("🔐 Loading from AWS Secrets Manager: %s (region: %s)\n", secretName, region)
	}

	ctx := context.TODO()
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := secretsmanager.NewFromConfig(awsCfg)
	result, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	})
	if err != nil {
		return fmt.Errorf("failed to get secret '%s': %w\nEnsure IAM role has secretsmanager:GetSecretValue permission", secretName, err)
	}

	// Unmarshal directly into the generic type
	if err := json.Unmarshal([]byte(*result.SecretString), cfg); err != nil {
		return fmt.Errorf("failed to unmarshal secret JSON: %w", err)
	}

	return nil
}

// populateFromEnv uses reflection to populate struct fields from environment variables
func populateFromEnv(cfg interface{}) error {
	v := reflect.ValueOf(cfg)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		// Handle embedded structs recursively
		if fieldType.Anonymous {
			if err := populateFromEnv(field.Addr().Interface()); err != nil {
				return err
			}
			continue
		}

		// Skip unexported fields
		if !field.CanSet() {
			continue
		}

		// Look for env tag
		envTag := fieldType.Tag.Get("env")
		if envTag == "" {
			// Try json tag as fallback
			jsonTag := fieldType.Tag.Get("json")
			if jsonTag != "" {
				parts := strings.Split(jsonTag, ",")
				envTag = strings.ToUpper(parts[0])
			}
		}

		if envTag == "" {
			continue
		}

		// Get value from environment
		envValue := os.Getenv(envTag)
		if envValue == "" {
			continue
		}

		// Set the field value based on type
		switch field.Kind() {
		case reflect.String:
			field.SetString(envValue)
		case reflect.Int, reflect.Int64:
			var intVal int64
			if _, err := fmt.Sscanf(envValue, "%d", &intVal); err == nil {
				field.SetInt(intVal)
			}
		case reflect.Bool:
			field.SetBool(strings.ToLower(envValue) == "true")
		}
	}

	return nil
}

// resolveRegion determines the AWS region from environment or defaults
func resolveRegion() string {
	if v := os.Getenv("AWS_REGION"); v != "" {
		return v
	}
	if v := os.Getenv("AWS_DEFAULT_REGION"); v != "" {
		return v
	}
	return "ap-south-1"
}

// RedactCredentials masks sensitive information in connection strings
func RedactCredentials(uri string) string {
	if uri == "" {
		return "<not set>"
	}

	// Handle URLs with credentials (e.g., mongodb://user:pass@host)
	if idx := strings.Index(uri, "://"); idx != -1 {
		prefix := uri[:idx+3]
		rest := uri[idx+3:]
		if atIdx := strings.Index(rest, "@"); atIdx != -1 {
			return prefix + "****:****@" + rest[atIdx+1:]
		}
	}

	// Handle DSN strings with password= (e.g., postgres)
	if strings.Contains(strings.ToLower(uri), "password=") {
		return "****"
	}

	// Generic redaction for long strings
	if len(uri) > 20 {
		return uri[:10] + "..." + uri[len(uri)-5:]
	}

	return "****"
}
