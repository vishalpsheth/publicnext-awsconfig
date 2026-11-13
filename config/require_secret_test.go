package awsconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	smithy "github.com/aws/smithy-go"
)

// ============================================================================
// LoadOptions.RequireSecret tests (fake seam only; zero real AWS calls)
// ============================================================================

const rsFileBody = "MONGO_URI=file-uri\nMONGO_DB=file-db\nCACHE_ADDR=file-cache\nAWS_REGION=r\nENVIRONMENT=file-env\n"

func rsOpts(t *testing.T, path string) LoadOptions {
	t.Helper()
	return LoadOptions{Precedence: PrecedenceSecretOverEnvOverFile, LocalPath: path, RequireSecret: true}
}

// 4) Not-found secret: RequireSecret makes every load a hard error, even for
// untagged structs with full env/file coverage; without it the fallback holds.
func TestRequireSecret_NotFound_HardError(t *testing.T) {
	file := writeEnvFile(t, rsFileBody)

	restore := withFakeClient(t, &fakeFetcher{err: &types.ResourceNotFoundException{}})
	defer restore()

	_, err := Load[precConfig]("my-secret", rsOpts(t, file))
	if !errors.Is(err, errSecretNotFound) {
		t.Fatalf("expected errSecretNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "RequireSecret is set") || !strings.Contains(err.Error(), "my-secret") {
		t.Fatalf("error should name the secret and RequireSecret: %v", err)
	}

	// Other struct shapes error too.
	if _, err := Load[CoreConfig]("sn", rsOpts(t, file)); !errors.Is(err, errSecretNotFound) {
		t.Fatalf("CoreConfig: expected errSecretNotFound, got %v", err)
	}
	if _, err := Load[soConfig]("sn", rsOpts(t, file)); !errors.Is(err, errSecretNotFound) {
		t.Fatalf("soConfig: expected errSecretNotFound, got %v", err)
	}

	// Without RequireSecret: silent empty-layer fallback (v1.3.0 behavior).
	opts := rsOpts(t, file)
	opts.RequireSecret = false
	cfg, err := Load[precConfig]("sn", opts)
	if err != nil || cfg.MongoURI != "file-uri" {
		t.Fatalf("fallback must remain without RequireSecret: cfg=%+v err=%v", cfg, err)
	}
}

func TestRequireSecret_NullBody_HardError(t *testing.T) {
	file := writeEnvFile(t, rsFileBody)

	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(`null`)})
	_, err := Load[precConfig]("sn", rsOpts(t, file))
	restore()
	if !errors.Is(err, errSecretMalformed) || !strings.Contains(err.Error(), "JSON null") {
		t.Fatalf("expected malformed JSON-null error, got %v", err)
	}

	// Without RequireSecret a null body keeps today's behavior: empty layer.
	restore = withFakeClient(t, &fakeFetcher{out: secretOutput(`null`)})
	defer restore()
	opts := rsOpts(t, file)
	opts.RequireSecret = false
	if _, err := Load[precConfig]("sn", opts); err != nil {
		t.Fatalf("null body must stay an empty layer without RequireSecret: %v", err)
	}
}

// An existing secret (even {}) satisfies RequireSecret; untagged fields then
// keep secret > env > file.
func TestRequireSecret_ExistingSecret_Loads(t *testing.T) {
	file := writeEnvFile(t, rsFileBody)
	t.Setenv("MONGO_DB", "env-db")

	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(`{"MONGO_URI":"sec-uri"}`)})
	cfg, err := Load[precConfig]("sn", rsOpts(t, file))
	restore()
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.MongoURI != "sec-uri" || cfg.MongoDB != "env-db" || cfg.RedisAddr != "file-cache" {
		t.Fatalf("precedence broken: %+v", cfg)
	}

	restore = withFakeClient(t, &fakeFetcher{out: secretOutput(`{}`)})
	defer restore()
	if _, err := Load[precConfig]("sn", rsOpts(t, file)); err != nil {
		t.Fatalf("an existing empty-object secret must satisfy RequireSecret: %v", err)
	}
}

// 5) Other fetch failures keep their errors under RequireSecret.
func TestRequireSecret_FetchErrors_Unchanged(t *testing.T) {
	file := writeEnvFile(t, rsFileBody)
	denied := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "no"}

	for name, tc := range map[string]struct {
		f    *fakeFetcher
		want error
	}{
		"access-denied": {&fakeFetcher{err: denied}, errSecretAccessDenied},
		"malformed":     {&fakeFetcher{out: secretOutput(`nope`)}, errSecretMalformed},
		"non-scalar":    {&fakeFetcher{out: secretOutput(`{"MONGO_URI":{"a":1}}`)}, errSecretMalformed},
	} {
		restore := withFakeClient(t, tc.f)
		_, err := Load[precConfig]("sn", rsOpts(t, file))
		restore()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: want %v got %v", name, tc.want, err)
		}
	}
}

// 7) ForceLocal && RequireSecret: contradiction before any I/O.
func TestRequireSecret_ForceLocal_Contradiction_BeforeIO(t *testing.T) {
	restoreFail := failIfClientBuilt(t)
	defer restoreFail()

	// An unparseable file would produce a different error if it were read.
	bad := filepath.Join(t.TempDir(), "bad.env")
	if err := os.WriteFile(bad, []byte("this is =not= valid\n\x00\"unterminated"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := rsOpts(t, bad)
	opts.ForceLocal = true
	_, err := Load[precConfig]("sn", opts)
	if err == nil || !strings.Contains(err.Error(), "RequireSecret contradicts ForceLocal") {
		t.Fatalf("expected contradiction error before file I/O, got %v", err)
	}
	if strings.Contains(err.Error(), "failed to parse") {
		t.Fatalf("file was read before the contradiction was detected: %v", err)
	}
}

func TestRequireSecret_Legacy_Unsupported_BeforeIO(t *testing.T) {
	restoreFail := failIfClientBuilt(t)
	defer restoreFail()

	for _, forceLocal := range []bool{false, true} {
		_, err := Load[precConfig]("sn", LoadOptions{
			RequireSecret: true,
			ForceLocal:    forceLocal,
			LocalPath:     filepath.Join(t.TempDir(), "never-read.env"),
		})
		if err == nil || !strings.Contains(err.Error(), "RequireSecret is not supported with PrecedenceLegacy") {
			t.Fatalf("forceLocal=%v: expected legacy error, got %v", forceLocal, err)
		}
	}
}

// An invalid Precedence still wins over RequireSecret.
func TestRequireSecret_InvalidPrecedence_StillInvalid(t *testing.T) {
	_, err := Load[precConfig]("sn", LoadOptions{Precedence: Precedence(99), RequireSecret: true, ForceLocal: true,
		LocalPath: nonExistentEnvPath(t)})
	if err == nil || !strings.Contains(err.Error(), "invalid Precedence") {
		t.Fatalf("got %v", err)
	}
}

// RequireSecret does not alter secret-only semantics when the secret exists.
func TestRequireSecret_WithSecretOnlyField(t *testing.T) {
	file := writeEnvFile(t, "ENVIRONMENT=file-env\n")
	t.Setenv("ENVIRONMENT", "env-env")

	restore := withFakeClient(t, &fakeFetcher{out: secretOutput(`{"ENVIRONMENT":"qa"}`)})
	cfg, err := Load[soConfig]("sn", rsOpts(t, file))
	restore()
	if err != nil || cfg.Environment != "qa" {
		t.Fatalf("got %+v err=%v", cfg, err)
	}

	restore = withFakeClient(t, &fakeFetcher{out: secretOutput(`{"MONGO_DB":"x"}`)})
	defer restore()
	_, err = Load[soConfig]("sn", rsOpts(t, file))
	if err == nil || !strings.Contains(err.Error(), "field ENVIRONMENT is secret-only but absent from secret") {
		t.Fatalf("got %v", err)
	}
}
