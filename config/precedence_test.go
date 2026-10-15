package awsconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	secretsmanager "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	smithy "github.com/aws/smithy-go"
)

// ============================================================================
// Test fakes & helpers
// ============================================================================

// fakeFetcher is an injectable secretsFetcher that returns canned output/errors
// to drive the REAL fetchSecretRaw + classifier + merge code.
type fakeFetcher struct {
	out      *secretsmanager.GetSecretValueOutput
	err      error
	calls    int
	errUntil int // if >0, return err for the first errUntil calls, then out
}

func (f *fakeFetcher) GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput,
	optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	f.calls++
	if f.errUntil > 0 && f.calls <= f.errUntil {
		return nil, f.err
	}
	if f.err != nil && f.errUntil == 0 {
		return nil, f.err
	}
	return f.out, nil
}

// withFakeClient swaps newSecretsClient to return the given fetcher and restores
// it when the returned func is deferred.
func withFakeClient(t *testing.T, f secretsFetcher) func() {
	t.Helper()
	prev := newSecretsClient
	newSecretsClient = func(ctx context.Context, region string) (secretsFetcher, error) {
		return f, nil
	}
	return func() { newSecretsClient = prev }
}

func secretOutput(body string) *secretsmanager.GetSecretValueOutput {
	return &secretsmanager.GetSecretValueOutput{SecretString: &body}
}

// writeEnvFile writes a temp .env file and returns its path.
func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return p
}

type precConfig struct {
	MongoURI    string        `json:"MONGO_URI" env:"MONGO_URI" required:"true"`
	MongoDB     string        `json:"MONGO_DB" env:"MONGO_DB" required:"true"`
	RedisAddr   string        `json:"CACHE_ADDR" env:"CACHE_ADDR" required:"true"`
	AWSRegion   string        `json:"AWS_REGION" env:"AWS_REGION" required:"true"`
	Environment string        `json:"ENVIRONMENT" env:"ENVIRONMENT" required:"true"`
	ServerPort  string        `json:"SERVER_PORT" env:"SERVER_PORT"`
	MaxRetries  int64         `json:"MAX_RETRIES,string" env:"MAX_RETRIES"`
	Debug       bool          `json:"DEBUG" env:"DEBUG"`
	Timeout     time.Duration `json:"TIMEOUT" env:"TIMEOUT"`
}

// nonExistentEnvPath returns a path with no file (so the file layer is empty).
func nonExistentEnvPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "does-not-exist.env")
}

// ============================================================================
// Real AWS-path tests (fake client drives fetchSecretRaw/classifier/merge)
// ============================================================================

func TestPerField_SuccessfulRetrieval(t *testing.T) {
	body := `{"MONGO_URI":"mongodb://s","MONGO_DB":"db","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"prod"}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()

	cfg, err := Load[precConfig]("sn", LoadOptions{
		Precedence: PrecedenceSecretOverEnvOverFile,
		LocalPath:  nonExistentEnvPath(t),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MongoURI != "mongodb://s" || cfg.Environment != "prod" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func TestPerField_MissingSecret_FallbackToEnvFile(t *testing.T) {
	restore := withFakeClient(t, &fakeFetcher{err: &types.ResourceNotFoundException{}})
	defer restore()

	envFile := writeEnvFile(t, "MONGO_URI=mongodb://file\nMONGO_DB=fdb\nCACHE_ADDR=fc\nAWS_REGION=fr\nENVIRONMENT=dev\n")
	cfg, err := Load[precConfig]("sn", LoadOptions{
		Precedence: PrecedenceSecretOverEnvOverFile,
		LocalPath:  envFile,
	})
	if err != nil {
		t.Fatalf("expected fallback success, got: %v", err)
	}
	if cfg.MongoURI != "mongodb://file" || cfg.Environment != "dev" {
		t.Fatalf("expected file-layer values, got: %+v", cfg)
	}
}

func TestPerField_AccessDenied_HardFail(t *testing.T) {
	apiErr := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "nope", Fault: smithy.FaultClient}
	restore := withFakeClient(t, &fakeFetcher{err: apiErr})
	defer restore()

	_, err := Load[precConfig]("sn", LoadOptions{
		Precedence: PrecedenceSecretOverEnvOverFile,
		LocalPath:  nonExistentEnvPath(t),
	})
	if err == nil {
		t.Fatal("expected access-denied hard fail")
	}
	if !errors.Is(err, errSecretAccessDenied) {
		t.Fatalf("expected errSecretAccessDenied, got: %v", err)
	}
}

func TestPerField_TransientRetryExhaustion_HardFail(t *testing.T) {
	prev := retryBaseDelay
	retryBaseDelay = 1 * time.Millisecond
	defer func() { retryBaseDelay = prev }()

	apiErr := &smithy.GenericAPIError{Code: "ThrottlingException", Fault: smithy.FaultServer}
	fake := &fakeFetcher{err: apiErr}
	restore := withFakeClient(t, fake)
	defer restore()

	_, err := Load[precConfig]("sn", LoadOptions{
		Precedence: PrecedenceSecretOverEnvOverFile,
		LocalPath:  nonExistentEnvPath(t),
	})
	if err == nil {
		t.Fatal("expected transient hard fail after retries")
	}
	if !errors.Is(err, errSecretTransient) {
		t.Fatalf("expected errSecretTransient, got: %v", err)
	}
	if fake.calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", fake.calls)
	}
}

func TestPerField_Timeout_Classified_Transient(t *testing.T) {
	prev := retryBaseDelay
	retryBaseDelay = 1 * time.Millisecond
	defer func() { retryBaseDelay = prev }()

	fake := &fakeFetcher{err: context.DeadlineExceeded}
	restore := withFakeClient(t, fake)
	defer restore()

	_, err := Load[precConfig]("sn", LoadOptions{
		Precedence: PrecedenceSecretOverEnvOverFile,
		LocalPath:  nonExistentEnvPath(t),
	})
	if err == nil || !errors.Is(err, errSecretTransient) {
		t.Fatalf("expected transient on deadline, got: %v", err)
	}
	if fake.calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", fake.calls)
	}
}

func TestPerField_InvalidJSON_HardFail(t *testing.T) {
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput("not json")})
	defer restore()

	_, err := Load[precConfig]("sn", LoadOptions{
		Precedence: PrecedenceSecretOverEnvOverFile,
		LocalPath:  nonExistentEnvPath(t),
	})
	if err == nil || !errors.Is(err, errSecretMalformed) {
		t.Fatalf("expected errSecretMalformed, got: %v", err)
	}
}

func TestPerField_WrongFieldType_HardFail(t *testing.T) {
	// MAX_RETRIES is int64 but secret sends a non-numeric string.
	body := `{"MONGO_URI":"m","MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p","MAX_RETRIES":"5abc"}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()

	_, err := Load[precConfig]("sn", LoadOptions{
		Precedence: PrecedenceSecretOverEnvOverFile,
		LocalPath:  nonExistentEnvPath(t),
	})
	if err == nil {
		t.Fatal("expected hard fail on wrong field type")
	}
	if !strings.Contains(err.Error(), "MAX_RETRIES") {
		t.Fatalf("expected error to name field, got: %v", err)
	}
	if strings.Contains(err.Error(), "5abc") {
		t.Fatalf("error leaked the token value: %v", err)
	}
}

func TestPerField_NilSecretString_HardFail_NoPanic(t *testing.T) {
	restore := withFakeClient(t, &fakeFetcher{out: &secretsmanager.GetSecretValueOutput{SecretString: nil}})
	defer restore()

	_, err := Load[precConfig]("sn", LoadOptions{
		Precedence: PrecedenceSecretOverEnvOverFile,
		LocalPath:  nonExistentEnvPath(t),
	})
	if err == nil || !errors.Is(err, errSecretMalformed) {
		t.Fatalf("expected errSecretMalformed on nil SecretString, got: %v", err)
	}
}

// ============================================================================
// Precedence / compatibility matrix
// ============================================================================

func TestPerField_SecretWinsOverEnvAndFile(t *testing.T) {
	body := `{"MONGO_URI":"from-secret","MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p"}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()

	os.Setenv("MONGO_URI", "from-env")
	defer os.Unsetenv("MONGO_URI")
	envFile := writeEnvFile(t, "MONGO_URI=from-file\n")

	cfg, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: envFile})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.MongoURI != "from-secret" {
		t.Fatalf("expected secret to win, got %q", cfg.MongoURI)
	}
}

func TestPerField_EnvWinsWhenSecretOmits(t *testing.T) {
	// Secret omits MONGO_URI.
	body := `{"MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p"}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()

	os.Setenv("MONGO_URI", "from-env")
	defer os.Unsetenv("MONGO_URI")
	envFile := writeEnvFile(t, "MONGO_URI=from-file\n")

	cfg, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: envFile})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.MongoURI != "from-env" {
		t.Fatalf("expected env to win, got %q", cfg.MongoURI)
	}
}

func TestPerField_FileWinsWhenSecretAndEnvOmit(t *testing.T) {
	body := `{"MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p"}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()

	os.Unsetenv("MONGO_URI")
	envFile := writeEnvFile(t, "MONGO_URI=from-file\n")

	cfg, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: envFile})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.MongoURI != "from-file" {
		t.Fatalf("expected file to win, got %q", cfg.MongoURI)
	}
}

func TestPerField_MissingEverywhere_ValidationError(t *testing.T) {
	// Required MONGO_URI missing everywhere.
	body := `{"MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p"}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()
	os.Unsetenv("MONGO_URI")

	_, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	if err == nil {
		t.Fatal("expected validation error for missing required field")
	}
	if !strings.Contains(err.Error(), "MONGO_URI") {
		t.Fatalf("expected error to name MONGO_URI, got: %v", err)
	}
}

func TestPerField_MissingOptional_LeavesZeroValue(t *testing.T) {
	body := `{"MONGO_URI":"m","MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p"}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()
	os.Unsetenv("SERVER_PORT")

	cfg, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.ServerPort != "" || cfg.MaxRetries != 0 {
		t.Fatalf("expected zero values for optional fields, got %+v", cfg)
	}
}

// ============================================================================
// Typed / presence semantics
// ============================================================================

func TestPerField_PresentFalseZeroEmpty_Authoritative(t *testing.T) {
	// Secret sets Debug=false, MaxRetries=0, ServerPort="" — all present and must
	// win over lower layers that set truthy/non-zero/non-empty values.
	body := `{"MONGO_URI":"m","MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p","DEBUG":false,"MAX_RETRIES":"0","SERVER_PORT":""}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()

	os.Setenv("DEBUG", "true")
	os.Setenv("MAX_RETRIES", "99")
	os.Setenv("SERVER_PORT", ":9999")
	defer func() {
		os.Unsetenv("DEBUG")
		os.Unsetenv("MAX_RETRIES")
		os.Unsetenv("SERVER_PORT")
	}()

	cfg, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.Debug != false {
		t.Fatalf("expected Debug false from secret, got %v", cfg.Debug)
	}
	if cfg.MaxRetries != 0 {
		t.Fatalf("expected MaxRetries 0 from secret, got %d", cfg.MaxRetries)
	}
	if cfg.ServerPort != "" {
		t.Fatalf("expected ServerPort empty from secret, got %q", cfg.ServerPort)
	}
}

func TestPerField_SecretNull_IsAbsent_NumericFallsThrough(t *testing.T) {
	// {"MAX_RETRIES": null} + env MAX_RETRIES=7 => 7 (null is absent, not 0).
	body := `{"MONGO_URI":"m","MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p","MAX_RETRIES":null}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()

	os.Setenv("MAX_RETRIES", "7")
	defer os.Unsetenv("MAX_RETRIES")

	cfg, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.MaxRetries != 7 {
		t.Fatalf("expected 7 (null absent, env wins), got %d", cfg.MaxRetries)
	}
}

func TestPerField_BareNumber_QuotedAndBare_BothDecode(t *testing.T) {
	// MAX_RETRIES as quoted "5".
	body1 := `{"MONGO_URI":"m","MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p","MAX_RETRIES":"5"}`
	restore1 := withFakeClient(t, &fakeFetcher{out: secretOutput(body1)})
	cfg1, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	restore1()
	if err != nil || cfg1.MaxRetries != 5 {
		t.Fatalf("quoted 5: got %v err %v", cfg1.MaxRetries, err)
	}

	// MAX_RETRIES as bare 5.
	body2 := `{"MONGO_URI":"m","MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p","MAX_RETRIES":5}`
	restore2 := withFakeClient(t, &fakeFetcher{out: secretOutput(body2)})
	cfg2, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	restore2()
	if err != nil || cfg2.MaxRetries != 5 {
		t.Fatalf("bare 5: got %v err %v", cfg2.MaxRetries, err)
	}
}

func TestPerField_BareNumberOnStringField_Accepted(t *testing.T) {
	// SERVER_PORT (string) receives bare 8080 -> "8080".
	body := `{"MONGO_URI":"m","MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p","SERVER_PORT":8080}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()

	cfg, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.ServerPort != "8080" {
		t.Fatalf("expected \"8080\", got %q", cfg.ServerPort)
	}
}

func TestPerField_BareNumberOnDuration_HardFail_UnitHint(t *testing.T) {
	body := `{"MONGO_URI":"m","MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p","TIMEOUT":30}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()

	_, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	if err == nil {
		t.Fatal("expected hard fail for bare number on duration field")
	}
	if !strings.Contains(err.Error(), "TIMEOUT") || !strings.Contains(err.Error(), "unit") {
		t.Fatalf("expected unit hint naming TIMEOUT, got: %v", err)
	}
}

func TestPerField_DurationWithUnit_OK(t *testing.T) {
	body := `{"MONGO_URI":"m","MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p","TIMEOUT":"30s"}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()

	cfg, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.Timeout != 30*time.Second {
		t.Fatalf("expected 30s, got %v", cfg.Timeout)
	}
}

func TestPerField_StrictBoolRejectsYes(t *testing.T) {
	body := `{"MONGO_URI":"m","MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p","DEBUG":"yes"}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()

	_, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	if err == nil {
		t.Fatal("expected hard fail for non-bool token \"yes\"")
	}
	if !strings.Contains(err.Error(), "DEBUG") {
		t.Fatalf("expected error to name DEBUG, got: %v", err)
	}
}

func TestPerField_EmptyObjectBody_FallsThrough_NoError(t *testing.T) {
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput("{}")})
	defer restore()

	envFile := writeEnvFile(t, "MONGO_URI=m\nMONGO_DB=d\nCACHE_ADDR=c\nAWS_REGION=r\nENVIRONMENT=p\n")
	cfg, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: envFile})
	if err != nil {
		t.Fatalf("expected {} to fall through, got: %v", err)
	}
	if cfg.MongoURI != "m" {
		t.Fatalf("expected file values after {} secret, got %+v", cfg)
	}
}

func TestPerField_EmptyStringBody_Malformed(t *testing.T) {
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput("")})
	defer restore()

	_, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	if err == nil || !errors.Is(err, errSecretMalformed) {
		t.Fatalf("expected errSecretMalformed for empty body, got: %v", err)
	}
}

func TestPerField_NonObjectBody_Malformed(t *testing.T) {
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(`["a","b"]`)})
	defer restore()

	_, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	if err == nil || !errors.Is(err, errSecretMalformed) {
		t.Fatalf("expected errSecretMalformed for array body, got: %v", err)
	}
}

// ============================================================================
// Embedded structs & custom Validator
// ============================================================================

func TestPerField_EmbeddedStruct(t *testing.T) {
	// StreamsConfig embeds CoreConfig.
	body := `{"MONGO_URI":"m","MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p","CONSUMER_NAME":"consumer-x","MAX_RETRIES":"4"}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()

	cfg, err := Load[StreamsConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.MongoURI != "m" || cfg.ConsumerName != "consumer-x" || cfg.MaxRetries != 4 {
		t.Fatalf("embedded merge failed: %+v", cfg)
	}
}

type validatorConfig struct {
	Name string `json:"NAME" env:"NAME"`
}

func (v *validatorConfig) Validate() error {
	if v.Name != "ok" {
		return errors.New("name must be ok")
	}
	return nil
}

func TestPerField_CustomValidator_Runs(t *testing.T) {
	body := `{"NAME":"bad"}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()

	_, err := Load[validatorConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	if err == nil || !strings.Contains(err.Error(), "name must be ok") {
		t.Fatalf("expected custom validator error, got: %v", err)
	}

	// Now satisfy the validator from the secret.
	body2 := `{"NAME":"ok"}`
	restore2 := withFakeClient(t, &fakeFetcher{out: secretOutput(body2)})
	defer restore2()
	cfg, err := Load[validatorConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	if err != nil || cfg.Name != "ok" {
		t.Fatalf("expected validator to pass, got cfg=%+v err=%v", cfg, err)
	}
}

// ============================================================================
// Secret safety: no leakage
// ============================================================================

func TestPerField_NoSecretLeak_InMalformedError(t *testing.T) {
	const leak = "SUPER_SECRET_VALUE_12345"
	body := `{"MONGO_URI":"m","MONGO_DB":"d","CACHE_ADDR":"c","AWS_REGION":"r","ENVIRONMENT":"p","MAX_RETRIES":"` + leak + `"}`
	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(body)})
	defer restore()

	_, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: nonExistentEnvPath(t)})
	if err == nil {
		t.Fatal("expected malformed int error")
	}
	if strings.Contains(err.Error(), leak) {
		t.Fatalf("error leaked secret value: %v", err)
	}
}

// ============================================================================
// Env contamination (NEW-mode loads only)
// ============================================================================

func TestPerField_NoEnvContamination(t *testing.T) {
	// Snapshot env, run a new-mode load reading a .env file, assert env unchanged.
	envFile := writeEnvFile(t, "MONGO_URI=file-uri\nMONGO_DB=d\nCACHE_ADDR=c\nAWS_REGION=r\nENVIRONMENT=p\n")
	restore := withFakeClient(t, &fakeFetcher{err: &types.ResourceNotFoundException{}})
	defer restore()

	os.Unsetenv("MONGO_URI")
	_, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: envFile})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	// The .env MONGO_URI must NOT have leaked into process env.
	if _, ok := os.LookupEnv("MONGO_URI"); ok {
		t.Fatal("new-mode load contaminated process env with MONGO_URI")
	}
}

func TestPerField_NoBleedBetweenLoads(t *testing.T) {
	restore := withFakeClient(t, &fakeFetcher{err: &types.ResourceNotFoundException{}})
	defer restore()
	os.Unsetenv("MONGO_URI")

	fileA := writeEnvFile(t, "MONGO_URI=a\nMONGO_DB=d\nCACHE_ADDR=c\nAWS_REGION=r\nENVIRONMENT=p\n")
	fileB := writeEnvFile(t, "MONGO_URI=b\nMONGO_DB=d\nCACHE_ADDR=c\nAWS_REGION=r\nENVIRONMENT=p\n")

	cfgA, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: fileA})
	if err != nil {
		t.Fatalf("load A: %v", err)
	}
	cfgB, err := Load[precConfig]("sn", LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: fileB})
	if err != nil {
		t.Fatalf("load B: %v", err)
	}
	if cfgA.MongoURI != "a" || cfgB.MongoURI != "b" {
		t.Fatalf("bleed between loads: A=%q B=%q", cfgA.MongoURI, cfgB.MongoURI)
	}
}

// ============================================================================
// ForceLocal new-mode & invalid Precedence
// ============================================================================

func TestPerField_ForceLocal_SkipsAWS_EnvOverFile(t *testing.T) {
	// Install a fake that would FAIL if called; ForceLocal must not call it.
	fake := &fakeFetcher{err: errors.New("should not be called")}
	restore := withFakeClient(t, fake)
	defer restore()

	envFile := writeEnvFile(t, "MONGO_URI=file-uri\nMONGO_DB=d\nCACHE_ADDR=c\nAWS_REGION=r\nENVIRONMENT=p\n")
	os.Setenv("MONGO_URI", "env-uri")
	defer os.Unsetenv("MONGO_URI")

	cfg, err := Load[precConfig]("sn", LoadOptions{
		Precedence: PrecedenceSecretOverEnvOverFile,
		ForceLocal: true,
		LocalPath:  envFile,
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if fake.calls != 0 {
		t.Fatalf("ForceLocal must not call AWS, got %d calls", fake.calls)
	}
	if cfg.MongoURI != "env-uri" {
		t.Fatalf("expected env > file under ForceLocal, got %q", cfg.MongoURI)
	}
}

func TestLoad_InvalidPrecedence_HardError_EvenWithForceLocal(t *testing.T) {
	_, err := Load[precConfig]("sn", LoadOptions{
		Precedence: Precedence(99),
		ForceLocal: true,
		LocalPath:  nonExistentEnvPath(t),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid Precedence") {
		t.Fatalf("expected invalid Precedence error, got: %v", err)
	}
}
