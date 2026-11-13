package awsconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	smithy "github.com/aws/smithy-go"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// ============================================================================
// secret:"only" tests. All AWS access goes through the fake seam; none is real.
// ============================================================================

type soConfig struct {
	Environment string `json:"ENVIRONMENT" env:"ENVIRONMENT" secret:"only" required:"true"`
	MongoDB     string `json:"MONGO_DB" env:"MONGO_DB"`
}

type soTyped struct {
	Count   int           `json:"SO_COUNT" env:"SO_COUNT" secret:"only"`
	Flag    bool          `json:"SO_FLAG" env:"SO_FLAG" secret:"only"`
	Timeout time.Duration `json:"SO_TIMEOUT" env:"SO_TIMEOUT" secret:"only"`
}

// soEmbeddedExported embeds an exported type, as CoreConfig-style structs do.
type SOCore struct {
	Environment string `json:"ENVIRONMENT" env:"ENVIRONMENT" secret:"only" required:"true"`
	MongoDB     string `json:"MONGO_DB" env:"MONGO_DB" required:"true"`
}

type soEmbeddedExported struct {
	SOCore
	Port string `json:"SERVER_PORT" env:"SERVER_PORT"`
}

func soOpts(t *testing.T, path string) LoadOptions {
	t.Helper()
	return LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: path}
}

// failIfClientBuilt makes any attempt to build an AWS client fail the test.
func failIfClientBuilt(t *testing.T) func() {
	t.Helper()
	prev := newSecretsClient
	newSecretsClient = func(ctx context.Context, region string) (secretsFetcher, error) {
		t.Errorf("newSecretsClient must not be invoked")
		return nil, errors.New("client must not be built")
	}
	return func() { newSecretsClient = prev }
}

// 1) Secret wins; process env and file are never consulted for a secret-only field.
func TestSecretOnly_SecretWinsOverEnvAndFile(t *testing.T) {
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(`{"ENVIRONMENT":"qa"}`)})
	defer restore()
	t.Setenv("ENVIRONMENT", "prod")
	file := writeEnvFile(t, "ENVIRONMENT=dev\n")

	cfg, err := Load[soConfig]("sn", soOpts(t, file))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.Environment != "qa" {
		t.Fatalf("expected secret value qa, got %q", cfg.Environment)
	}
}

func TestSecretOnly_StrictTypeParsing(t *testing.T) {
	good := `{"SO_COUNT":3,"SO_FLAG":true,"SO_TIMEOUT":"2s"}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(good)})
	cfg, err := Load[soTyped]("sn", soOpts(t, nonExistentEnvPath(t)))
	restore()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.Count != 3 || !cfg.Flag || cfg.Timeout != 2*time.Second {
		t.Fatalf("bad parse: %+v", cfg)
	}

	for name, body := range map[string]string{
		"int":      `{"SO_COUNT":"abc","SO_FLAG":true,"SO_TIMEOUT":"2s"}`,
		"bool":     `{"SO_COUNT":1,"SO_FLAG":"yes","SO_TIMEOUT":"2s"}`,
		"duration": `{"SO_COUNT":1,"SO_FLAG":true,"SO_TIMEOUT":5}`,
	} {
		restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
		_, err := Load[soTyped]("sn", soOpts(t, nonExistentEnvPath(t)))
		restore()
		if err == nil || !strings.Contains(err.Error(), "from secret") {
			t.Fatalf("%s: expected strict parse error, got %v", name, err)
		}
	}
}

// 2) Key absent from the secret is a hard error, even with env/file values.
func TestSecretOnly_AbsentKey_Error_NoEnvFileFallback(t *testing.T) {
	const envVal, fileVal = "ENVVAL_SENTINEL_1", "FILEVAL_SENTINEL_2"
	for name, tc := range map[string]struct{ env, file string }{
		"env":  {env: envVal},
		"file": {file: "ENVIRONMENT=" + fileVal + "\n"},
		"both": {env: envVal, file: "ENVIRONMENT=" + fileVal + "\n"},
	} {
		t.Run(name, func(t *testing.T) {
			restore := withFakeClient(t, &fakeFetcher{out: secretOutput(`{"MONGO_DB":"db"}`)})
			defer restore()
			if tc.env != "" {
				t.Setenv("ENVIRONMENT", tc.env)
			} else {
				t.Setenv("ENVIRONMENT", "")
				os.Unsetenv("ENVIRONMENT")
			}
			path := nonExistentEnvPath(t)
			if tc.file != "" {
				path = writeEnvFile(t, tc.file)
			}
			cfg, err := Load[soConfig]("sn", soOpts(t, path))
			if err == nil {
				t.Fatalf("expected error, got cfg %+v", cfg)
			}
			if !strings.Contains(err.Error(), "field ENVIRONMENT is secret-only but absent from secret") {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(err.Error(), envVal) || strings.Contains(err.Error(), fileVal) {
				t.Fatalf("error leaked a value: %v", err)
			}
		})
	}
}

// 3) null / missing => error; "" present => accepted as an empty value.
func TestSecretOnly_NullMissingEmpty(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"missing", `{"MONGO_DB":"x"}`, true},
		{"json-null-value", `{"ENVIRONMENT":null}`, true},
		{"empty-object", `{}`, true},
		{"present-empty-string", `{"ENVIRONMENT":""}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restore := withFakeClient(t, &fakeFetcher{out: secretOutput(tc.body)})
			defer restore()
			t.Setenv("ENVIRONMENT", "from-env")
			cfg, err := Load[soConfig]("sn", LoadOptions{
				Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t), SkipValidation: true,
			})
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "secret-only but absent") {
					t.Fatalf("expected absent error, got cfg=%v err=%v", cfg, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if cfg.Environment != "" {
				t.Fatalf("present empty string must win over env, got %q", cfg.Environment)
			}
		})
	}

	// With validation on, a present "" on a required field is still rejected by
	// the existing required check: the consumer-visible semantics are unchanged.
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(`{"ENVIRONMENT":""}`)})
	defer restore()
	_, err := Load[soConfig]("sn", soOpts(t, nonExistentEnvPath(t)))
	if err == nil || !strings.Contains(err.Error(), "missing required fields") {
		t.Fatalf("expected required-field validation error, got %v", err)
	}
}

// A JSON-null body yields a nil map (same as not-found); the secret was still
// attempted, so a secret-only field must error rather than fall through.
func TestSecretOnly_NullBody_Errors(t *testing.T) {
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(`null`)})
	defer restore()
	t.Setenv("ENVIRONMENT", "from-env")
	_, err := Load[soConfig]("sn", soOpts(t, nonExistentEnvPath(t)))
	if err == nil || !strings.Contains(err.Error(), "secret-only but absent") {
		t.Fatalf("expected absent error, got %v", err)
	}
}

// 4) Not-found secret.
func TestSecretOnly_SecretNotFound(t *testing.T) {
	t.Setenv("ENVIRONMENT", "from-env")
	t.Setenv("MONGO_DB", "env-db")

	// Secret-only field => hard error even though env has a value.
	restore := withFakeClient(t, &fakeFetcher{err: &types.ResourceNotFoundException{}})
	_, err := Load[soConfig]("sn", soOpts(t, nonExistentEnvPath(t)))
	restore()
	if err == nil || !strings.Contains(err.Error(), "field ENVIRONMENT is secret-only but absent from secret") {
		t.Fatalf("expected secret-only error, got %v", err)
	}

	// Without any secret-only field, the v1.3.0 empty-layer fallback is intact.
	restore = withFakeClient(t, &fakeFetcher{err: &types.ResourceNotFoundException{}})
	defer restore()
	cfg, err := Load[precConfig]("sn", LoadOptions{
		Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: writeEnvFile(t,
			"MONGO_URI=u\nMONGO_DB=d\nCACHE_ADDR=c\nAWS_REGION=r\nENVIRONMENT=e\n"),
	})
	if err != nil {
		t.Fatalf("fallback must still work for untagged structs: %v", err)
	}
	if cfg.MongoDB != "env-db" {
		t.Fatalf("expected env > file fallback, got %q", cfg.MongoDB)
	}
}

// 5) Fetch/classification failures are unchanged with the tag present.
func TestSecretOnly_FetchErrors_Unchanged(t *testing.T) {
	prev := retryBaseDelay
	retryBaseDelay = time.Millisecond
	defer func() { retryBaseDelay = prev }()

	cases := []struct {
		name string
		f    *fakeFetcher
		want error
	}{
		{"access-denied", &fakeFetcher{err: &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "no"}}, errSecretAccessDenied},
		{"transient-exhausted", &fakeFetcher{err: errors.New("boom")}, errSecretTransient},
		{"malformed-json", &fakeFetcher{out: secretOutput(`not json`)}, errSecretMalformed},
		{"array-body", &fakeFetcher{out: secretOutput(`[]`)}, errSecretMalformed},
		{"non-scalar-value", &fakeFetcher{out: secretOutput(`{"ENVIRONMENT":{"a":1}}`)}, errSecretMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restore := withFakeClient(t, tc.f)
			defer restore()
			_, err := Load[soConfig]("sn", soOpts(t, nonExistentEnvPath(t)))
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

// 6) ForceLocal: tag ignored, env > file, no client built, zero fetches.
func TestSecretOnly_ForceLocal_TagIgnored(t *testing.T) {
	restoreFail := failIfClientBuilt(t)
	defer restoreFail()
	t.Setenv("ENVIRONMENT", "env-wins")
	file := writeEnvFile(t, "ENVIRONMENT=file-loses\nMONGO_DB=file-db\n")

	opts := soOpts(t, file)
	opts.ForceLocal = true
	cfg, err := Load[soConfig]("sn", opts)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.Environment != "env-wins" || cfg.MongoDB != "file-db" {
		t.Fatalf("expected env > file, got %+v", cfg)
	}

	// File-only also resolves.
	t.Setenv("ENVIRONMENT", "")
	os.Unsetenv("ENVIRONMENT")
	cfg, err = Load[soConfig]("sn", opts)
	if err != nil || cfg.Environment != "file-loses" {
		t.Fatalf("expected file value under ForceLocal, got %+v err=%v", cfg, err)
	}
}

// 8) Invalid tag values are hard configuration errors, also under ForceLocal.
func TestSecretTag_InvalidValue_HardError(t *testing.T) {
	type bad struct {
		X string `json:"X_KEY" env:"X_KEY" secret:"onyl"`
	}
	for _, forceLocal := range []bool{false, true} {
		t.Run(fmt.Sprintf("forceLocal=%v", forceLocal), func(t *testing.T) {
			restore := withFakeClient(t, &fakeFetcher{out: secretOutput(`{"X_KEY":"v"}`)})
			defer restore()
			opts := soOpts(t, nonExistentEnvPath(t))
			opts.ForceLocal = forceLocal
			_, err := Load[bad]("sn", opts)
			if err == nil || !strings.Contains(err.Error(), "field X has invalid secret tag value") {
				t.Fatalf("expected invalid tag error, got %v", err)
			}
			if strings.Contains(err.Error(), "onyl") {
				t.Fatalf("error should not echo the tag value: %v", err)
			}
		})
	}
}

func TestSecretTag_OnlyWithoutKey_HardError(t *testing.T) {
	type nokey struct {
		X string `secret:"only"`
	}
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(`{}`)})
	defer restore()
	_, err := Load[nokey]("sn", soOpts(t, nonExistentEnvPath(t)))
	if err == nil || !strings.Contains(err.Error(), "has no env/json key") {
		t.Fatalf("expected no-key error, got %v", err)
	}
}

// 8) Untagged fields keep secret > env > file next to a secret-only field.
func TestSecretOnly_UntaggedFieldsKeepPrecedence(t *testing.T) {
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(`{"ENVIRONMENT":"qa","MONGO_DB":"sec-db"}`)})
	defer restore()
	t.Setenv("MONGO_DB", "env-db")
	file := writeEnvFile(t, "MONGO_DB=file-db\n")
	cfg, err := Load[soConfig]("sn", soOpts(t, file))
	if err != nil || cfg.MongoDB != "sec-db" || cfg.Environment != "qa" {
		t.Fatalf("got %+v err=%v", cfg, err)
	}

	// Secret omits MONGO_DB: untagged field still falls to env, then file.
	restore2 := withFakeClient(t, &fakeFetcher{out: secretOutput(`{"ENVIRONMENT":"qa"}`)})
	defer restore2()
	cfg, err = Load[soConfig]("sn", soOpts(t, file))
	if err != nil || cfg.MongoDB != "env-db" {
		t.Fatalf("expected env fallback, got %+v err=%v", cfg, err)
	}
	t.Setenv("MONGO_DB", "")
	os.Unsetenv("MONGO_DB")
	cfg, err = Load[soConfig]("sn", soOpts(t, file))
	if err != nil || cfg.MongoDB != "file-db" {
		t.Fatalf("expected file fallback, got %+v err=%v", cfg, err)
	}
}

// Legacy ignores the tag: the legacy path is untouched. ForceLocal legacy
// populates from env/file regardless of the tag and makes no AWS client.
func TestSecretOnly_Legacy_TagIgnored(t *testing.T) {
	restoreFail := failIfClientBuilt(t)
	defer restoreFail()
	file := writeEnvFile(t, "ENVIRONMENT=legacy-file\n")
	t.Setenv("ENVIRONMENT", "")
	os.Unsetenv("ENVIRONMENT")
	t.Cleanup(func() { os.Unsetenv("ENVIRONMENT") }) // godotenv.Load mutates env

	cfg, err := Load[soConfig]("sn", LoadOptions{ForceLocal: true, LocalPath: file})
	if err != nil || cfg.Environment != "legacy-file" {
		t.Fatalf("legacy must ignore the tag, got %+v err=%v", cfg, err)
	}

	// An invalid tag value is NOT validated by the legacy path (unchanged).
	type bad struct {
		X string `json:"X_KEY" env:"X_KEY" secret:"bogus"`
	}
	t.Setenv("X_KEY", "v")
	if _, err := Load[bad]("sn", LoadOptions{ForceLocal: true, LocalPath: file}); err != nil {
		t.Fatalf("legacy must not validate the secret tag: %v", err)
	}
}

// 9) A sentinel secret value never appears in errors or captured logs.
func TestSecretOnly_NoValueLeak_ErrorsAndLogs(t *testing.T) {
	const leak = "LEAK_SENTINEL_9f3a7c"
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)

	bodies := map[string]string{
		"bad-int-secret-only": `{"SO_COUNT":"` + leak + `","SO_FLAG":true,"SO_TIMEOUT":"1s"}`,
		"bad-bool":            `{"SO_COUNT":1,"SO_FLAG":"` + leak + `","SO_TIMEOUT":"1s"}`,
		"bad-duration":        `{"SO_COUNT":1,"SO_FLAG":true,"SO_TIMEOUT":"` + leak + `"}`,
		"non-scalar":          `{"SO_COUNT":{"x":"` + leak + `"}}`,
		"missing-key":         `{"OTHER":"` + leak + `"}`,
		"malformed":           `{"SO_COUNT":"` + leak,
		"valid":               `{"SO_COUNT":1,"SO_FLAG":true,"SO_TIMEOUT":"1s","OTHER":"` + leak + `"}`,
	}
	for name, body := range bodies {
		restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
		opts := soOpts(t, nonExistentEnvPath(t))
		opts.Logger = logger
		_, err := Load[soTyped]("sn", opts)
		restore()
		if err != nil && strings.Contains(err.Error(), leak) {
			t.Fatalf("%s: error leaked value: %v", name, err)
		}
	}
	// Also via soConfig (string secret-only field holding the sentinel is
	// returned to the caller by design, but must not be logged).
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(`{"ENVIRONMENT":"` + leak + `"}`)})
	opts := soOpts(t, nonExistentEnvPath(t))
	opts.Logger = logger
	_, _ = Load[soConfig]("sn", opts)
	restore()

	if logs.Len() == 0 {
		t.Fatal("expected some log entries to inspect")
	}
	for _, e := range logs.All() {
		if strings.Contains(e.Message, leak) {
			t.Fatalf("log message leaked value: %q", e.Message)
		}
		for k, v := range e.ContextMap() {
			if strings.Contains(fmt.Sprint(k, v), leak) {
				t.Fatalf("log field leaked value: %s=%v", k, v)
			}
		}
	}
}

// 10) Embedded structs (unexported and exported embeds) honor the tag.
func TestSecretOnly_EmbeddedStruct(t *testing.T) {
	t.Setenv("ENVIRONMENT", "from-env")
	t.Setenv("SERVER_PORT", ":9999")

	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(`{"ENVIRONMENT":"qa","MONGO_DB":"db"}`)})
	defer restore()
	cfg, err := Load[soEmbeddedExported]("sn", soOpts(t, nonExistentEnvPath(t)))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.Environment != "qa" || cfg.Port != ":9999" {
		t.Fatalf("got %+v", cfg)
	}

	// Missing in the secret: the embedded tagged field errors by key name.
	restore2 := withFakeClient(t, &fakeFetcher{out: secretOutput(`{"MONGO_DB":"db"}`)})
	defer restore2()
	_, err = Load[soEmbeddedExported]("sn", soOpts(t, nonExistentEnvPath(t)))
	if err == nil || !strings.Contains(err.Error(), "field ENVIRONMENT is secret-only but absent from secret") {
		t.Fatalf("expected embedded secret-only error, got %v", err)
	}
	if strings.Contains(err.Error(), "from-env") {
		t.Fatalf("leaked env value: %v", err)
	}

	// ForceLocal through the embed: tag ignored, env wins, no AWS client.
	restore3 := failIfClientBuilt(t)
	defer restore3()
	opts := soOpts(t, nonExistentEnvPath(t))
	opts.ForceLocal = true
	t.Setenv("MONGO_DB", "env-db")
	cfg, err = Load[soEmbeddedExported]("sn", opts)
	if err != nil || cfg.Environment != "from-env" {
		t.Fatalf("got %+v err=%v", cfg, err)
	}
}

// 10) Validator and required:"true" interplay is unchanged for tagged types.
type soValidated struct {
	Environment string `json:"ENVIRONMENT" env:"ENVIRONMENT" secret:"only"`
}

func (v *soValidated) Validate() error {
	if v.Environment != "qa" {
		return errors.New("environment must be qa")
	}
	return nil
}

func TestSecretOnly_ValidatorAndRequired_Unchanged(t *testing.T) {
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(`{"ENVIRONMENT":"prod"}`)})
	_, err := Load[soValidated]("sn", soOpts(t, nonExistentEnvPath(t)))
	restore()
	if err == nil || !strings.Contains(err.Error(), "environment must be qa") {
		t.Fatalf("expected validator error, got %v", err)
	}
	restore = withFakeClient(t, &fakeFetcher{out: secretOutput(`{"ENVIRONMENT":"qa"}`)})
	cfg, err := Load[soValidated]("sn", soOpts(t, nonExistentEnvPath(t)))
	restore()
	if err != nil || cfg.Environment != "qa" {
		t.Fatalf("got %+v err=%v", cfg, err)
	}
	// SkipValidation skips the validator as before.
	restore = withFakeClient(t, &fakeFetcher{out: secretOutput(`{"ENVIRONMENT":"prod"}`)})
	defer restore()
	opts := soOpts(t, nonExistentEnvPath(t))
	opts.SkipValidation = true
	if _, err := Load[soValidated]("sn", opts); err != nil {
		t.Fatalf("SkipValidation: %v", err)
	}
}
