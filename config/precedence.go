package awsconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	secretsmanager "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	smithy "github.com/aws/smithy-go"
	"github.com/joho/godotenv"
	"go.uber.org/zap"
)

// Error sentinels for the new per-field precedence mode. All unexported: the
// only public surface added by this feature is Precedence + the LoadOptions
// field.
var (
	errSecretNotFound     = errors.New("awsconfig: secret not found")                      // ResourceNotFoundException
	errSecretAccessDenied = errors.New("awsconfig: access denied to secret")               //
	errSecretTransient    = errors.New("awsconfig: secret retrieval failed after retries") //
	errSecretMalformed    = errors.New("awsconfig: secret JSON invalid")                   //
)

// retryBaseDelay is the base backoff between SM fetch attempts in the new mode.
// Production uses 1s (so attempts back off 1s then 2s). Tests lower it (and
// restore via defer) so retry-exhaustion runs in milliseconds. Unexported seam.
var retryBaseDelay = 1 * time.Second

// secretsFetcher captures exactly the one Secrets Manager method the new mode
// uses. Its signature matches *secretsmanager.Client.GetSecretValue exactly, so
// the real client satisfies it with no adapter, and tests can inject a fake.
type secretsFetcher interface {
	GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput,
		optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// newSecretsClient builds the real SM client for the new mode. It disables
// SDK-internal retries (WithRetryMaxAttempts(1)) so the design's own bounded
// outer loop is the single, authoritative retry mechanism. Overridable in tests
// only; production code never reassigns it.
var newSecretsClient = func(ctx context.Context, region string) (secretsFetcher, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithRetryMaxAttempts(1), // outer loop owns retries
	)
	if err != nil {
		return nil, err
	}
	return secretsmanager.NewFromConfig(awsCfg), nil
}

// classifySecretErr maps an SM GetSecretValue error to one of the unexported
// sentinels. It is total: every error reaches exactly one branch. It never
// sniffs raw error strings.
func classifySecretErr(err error) error {
	// 1. Not found -> permit fallback (terminal, not retried).
	var notFound *types.ResourceNotFoundException
	if errors.As(err, &notFound) {
		return errSecretNotFound
	}
	// 2. Authorization / throttling failures arrive as a generic smithy API
	//    error with a string code (there is no AccessDeniedException or
	//    ThrottlingException struct in secretsmanager@v1.41.1). The service may
	//    emit any of the access-denied aliases below depending on the exact
	//    authz failure, so the switch matches the full known set.
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "AccessDeniedException", "AccessDenied", "UnrecognizedClientException":
			return errSecretAccessDenied
		case "ThrottlingException", "ThrottledException", "TooManyRequestsException":
			return errSecretTransient // only hard-fails after retry exhaustion
		}
	}
	// 3. Context deadline / timeout.
	if errors.Is(err, context.DeadlineExceeded) {
		return errSecretTransient
	}
	// 4. Everything else (InternalServiceError, DecryptionFailure, unknown
	//    transport errors, and any unmatched API code) -> transient. An
	//    unrecognized code is intentionally retried 3x before hard-failing
	//    rather than failing fast; add future fail-fast codes to the
	//    access-denied case above. Total: no silent fall-through.
	return errSecretTransient
}

// fetchSecretRaw is the single SM fetch for the new mode. It runs a bounded
// outer retry loop (3 attempts, exponential backoff retryBaseDelay*1<<n) with a
// fresh per-attempt timeout, classifies errors, and returns the secret parsed
// into a raw map so per-key presence is preserved. It never dereferences a nil
// SecretString and never includes any secret value in an error or log.
func fetchSecretRaw(ctx context.Context, client secretsFetcher, secretName string,
	logger *zap.Logger) (map[string]json.RawMessage, error) {

	logInfo(logger, "Loading from AWS Secrets Manager (per-field mode)", secretName)

	const maxRetries = 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := retryBaseDelay * time.Duration(1<<(attempt-1)) // 1s, 2s in prod
			logInfo(logger, fmt.Sprintf("Retry %d/%d after %s", attempt+1, maxRetries, delay))
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("%w: %v", errSecretTransient, ctx.Err())
			case <-time.After(delay):
			}
		}

		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		out, err := client.GetSecretValue(attemptCtx, &secretsmanager.GetSecretValueInput{
			SecretId: aws.String(secretName),
		})
		cancel()

		if err != nil {
			classified := classifySecretErr(err)
			switch {
			case errors.Is(classified, errSecretNotFound):
				// Whole secret absent -> permit fallback. Terminal.
				return nil, errSecretNotFound
			case errors.Is(classified, errSecretAccessDenied):
				// Authorization failure. Terminal hard fail (no value leaked).
				return nil, fmt.Errorf("%w (secret %q)", errSecretAccessDenied, secretName)
			default:
				// Transient: retry until exhaustion.
				lastErr = classified
				continue
			}
		}

		// Successful call. Check for nil string body BEFORE any dereference.
		if out.SecretString == nil {
			return nil, fmt.Errorf("%w: secret has no string body", errSecretMalformed)
		}

		raw := make(map[string]json.RawMessage)
		if uErr := json.Unmarshal([]byte(*out.SecretString), &raw); uErr != nil {
			// Empty string, whitespace, array, scalar, or any non-object body.
			// Never include the body in the error.
			return nil, fmt.Errorf("%w: body is not a JSON object", errSecretMalformed)
		}
		return raw, nil
	}

	// Retries exhausted on a transient class.
	return nil, fmt.Errorf("%w (secret %q, retried %d times): %v",
		errSecretTransient, secretName, maxRetries, lastErr)
}

// resolveKey derives a field's resolution key from its struct tags, replicating
// populateFromEnv's derivation character-for-character so file/env/secret all
// index identically and the new mode matches legacy key behavior: the env tag
// verbatim if present, otherwise strings.ToUpper(strings.Split(jsonTag, ",")[0])
// (comma-split BEFORE uppercasing).
func resolveKey(fieldType reflect.StructField) string {
	envTag := fieldType.Tag.Get("env")
	if envTag != "" {
		return envTag
	}
	jsonTag := fieldType.Tag.Get("json")
	if jsonTag == "" {
		return ""
	}
	return strings.ToUpper(strings.Split(jsonTag, ",")[0])
}

// rawLayer maps a field resolution key to its present string token. Presence is
// tracked by key existence in the map, independent of the token's content.
type rawLayer map[string]string

// loadFileLayer reads the .env file independently into a map via godotenv.Read
// (NOT godotenv.Load, which mutates os.Environ). A missing file yields an empty
// layer and is not an error; an unparseable file is a hard error. File keys are
// matched exactly, with NO case normalization (mirrors case-sensitive os.Getenv).
func loadFileLayer(path string, logger *zap.Logger) (rawLayer, error) {
	m, err := godotenv.Read(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logInfo(logger, "Local config file not found; file layer empty", path)
			return rawLayer{}, nil
		}
		return nil, fmt.Errorf("failed to parse local config file %s: %w", path, err)
	}
	layer := make(rawLayer, len(m))
	for k, v := range m {
		layer[k] = v
	}
	return layer, nil
}

// secretTokenForKey normalizes a present raw secret value to a decode token per
// the total ordered rule. The second return reports presence: a key that is
// absent, or whose raw value is JSON null, is NOT present and permits fallback.
func secretTokenForKey(raw map[string]json.RawMessage, key string) (string, bool, error) {
	rawVal, ok := raw[key]
	if !ok {
		return "", false, nil // absent
	}
	trimmed := strings.TrimSpace(string(rawVal))
	if trimmed == "null" {
		return "", false, nil // null -> absent, permits fallback
	}
	// Try string first so quoted values (incl. the json:",string" convention and
	// explicit "") unwrap verbatim.
	var s string
	if err := json.Unmarshal(rawVal, &s); err == nil {
		return s, true, nil
	}
	// Bare JSON number -> canonical string form.
	var f float64
	if err := json.Unmarshal(rawVal, &f); err == nil {
		return strconv.FormatFloat(f, 'f', -1, 64), true, nil
	}
	// Bare JSON bool.
	var b bool
	if err := json.Unmarshal(rawVal, &b); err == nil {
		return strconv.FormatBool(b), true, nil
	}
	// Object/array for a scalar field, or otherwise undecodable.
	return "", false, fmt.Errorf("%w: field %s has a non-scalar value in secret", errSecretMalformed, key)
}

// setFieldFromString decodes token into field using STRICT parsers. It covers
// the same set of supported kinds as populateFromEnv but hard-fails on any parse
// error, naming the field key and source layer and NEVER the token value.
func setFieldFromString(field reflect.Value, token, key, layer string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(token)
	case reflect.Int, reflect.Int64:
		if field.Type() == reflect.TypeOf(time.Duration(0)) {
			d, err := time.ParseDuration(token)
			if err != nil {
				return fmt.Errorf("field %s from %s is not a valid duration (requires a unit, e.g. \"30s\")", key, layer)
			}
			field.SetInt(int64(d))
			return nil
		}
		n, err := strconv.ParseInt(token, 10, 64)
		if err != nil {
			return fmt.Errorf("field %s from %s is not a valid integer", key, layer)
		}
		field.SetInt(n)
	case reflect.Uint, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(token, 10, 64)
		if err != nil {
			return fmt.Errorf("field %s from %s is not a valid unsigned integer", key, layer)
		}
		field.SetUint(n)
	case reflect.Float64:
		f, err := strconv.ParseFloat(token, 64)
		if err != nil {
			return fmt.Errorf("field %s from %s is not a valid float", key, layer)
		}
		field.SetFloat(f)
	case reflect.Bool:
		b, err := strconv.ParseBool(token)
		if err != nil {
			return fmt.Errorf("field %s from %s is not a valid boolean", key, layer)
		}
		field.SetBool(b)
	default:
		return fmt.Errorf("field %s from %s has unsupported kind %s", key, layer, field.Kind())
	}
	return nil
}

// mergeFields walks the struct (recursing into anonymous embedded structs
// exactly as populateFromEnv/validateRequiredFields do) and, for each field,
// selects the highest PRESENT layer (secret > env > file) and decodes it. A
// field absent from all layers is left at its Go zero value (no Load-time
// defaulting).
func mergeFields(cfgPtr interface{}, file rawLayer, secret map[string]json.RawMessage) error {
	v := reflect.ValueOf(cfgPtr)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		// Recurse into embedded structs.
		if fieldType.Anonymous {
			if err := mergeFields(field.Addr().Interface(), file, secret); err != nil {
				return err
			}
			continue
		}

		if !field.CanSet() {
			continue
		}

		key := resolveKey(fieldType)
		if key == "" {
			continue
		}

		// Secret (highest).
		if secret != nil {
			token, present, err := secretTokenForKey(secret, key)
			if err != nil {
				return err
			}
			if present {
				if err := setFieldFromString(field, token, key, "secret"); err != nil {
					return err
				}
				continue
			}
		}

		// Env (middle). LookupEnv distinguishes present-and-empty from unset.
		if token, ok := os.LookupEnv(key); ok {
			if err := setFieldFromString(field, token, key, "environment"); err != nil {
				return err
			}
			continue
		}

		// File (lowest). Exact-match, no case normalization.
		if token, ok := file[key]; ok {
			if err := setFieldFromString(field, token, key, "file"); err != nil {
				return err
			}
			continue
		}

		// Absent everywhere: leave Go zero value.
	}
	return nil
}

// loadPerField implements the opt-in per-field precedence mode: file, process
// environment, and Secrets Manager JSON loaded independently and merged per
// field (secret > env > file), then validated via the existing mechanism.
func loadPerField[T any](secretName string, options LoadOptions) (*T, error) {
	var cfg T

	// Layer 1 — file (lowest), read without mutating os.Environ.
	fileLayer, err := loadFileLayer(options.LocalPath, options.Logger)
	if err != nil {
		return nil, err
	}

	// Layer 3 — Secrets Manager JSON (highest). Skipped entirely under
	// ForceLocal (no client built, no network). ResourceNotFoundException is
	// treated as an empty secret layer (permits fallback); access-denied,
	// transient, and malformed are hard fails.
	var secretLayer map[string]json.RawMessage
	if !options.ForceLocal {
		ctx := context.Background()
		client, cErr := newSecretsClient(ctx, options.AWSRegion)
		if cErr != nil {
			return nil, fmt.Errorf("failed to build AWS client: %w", cErr)
		}
		raw, fErr := fetchSecretRaw(ctx, client, secretName, options.Logger)
		if fErr != nil {
			if errors.Is(fErr, errSecretNotFound) {
				logInfo(options.Logger, "Secret not found; falling back to env/file", secretName)
				secretLayer = nil
			} else {
				return nil, fErr
			}
		} else {
			secretLayer = raw
		}
	}

	// Layer 2 (env) is read per field inside mergeFields via os.LookupEnv.
	if err := mergeFields(&cfg, fileLayer, secretLayer); err != nil {
		return nil, err
	}

	// Validate via the EXISTING mechanism, gated by SkipValidation.
	if !options.SkipValidation {
		if err := validateConfig(&cfg); err != nil {
			return nil, fmt.Errorf("validation failed: %w", err)
		}
	}

	return &cfg, nil
}
