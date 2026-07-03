package awsconfig

import (
	"os"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// EXPLORATION TESTS — These test BUG CONDITIONS (expected to FAIL on unfixed code)
// ============================================================================

// Test 1b: Validation rejects optional empty field (bug)
func TestExploration_ValidationRejectsOptionalField(t *testing.T) {
	type TestConfig struct {
		Required string `json:"REQUIRED" env:"REQUIRED" required:"true"`
		Optional string `json:"OPTIONAL" env:"OPTIONAL"` // no required tag
	}
	cfg := &TestConfig{Required: "has-value", Optional: ""}
	err := validateRequiredFields(cfg)
	// BUG: current code rejects this because Optional has json/env tag but is empty
	// FIXED: should pass because no required:"true" tag
	if err != nil {
		t.Fatalf("BUG: validateRequiredFields rejected optional empty field: %v", err)
	}
}

// Test 1c: Duration type not parsed (bug)
func TestExploration_DurationNotParsed(t *testing.T) {
	type TestConfig struct {
		Timeout time.Duration `env:"TEST_TIMEOUT"`
	}
	os.Setenv("TEST_TIMEOUT", "30s")
	defer os.Unsetenv("TEST_TIMEOUT")

	cfg := &TestConfig{}
	populateFromEnv(cfg)

	if cfg.Timeout != 30*time.Second {
		t.Fatalf("BUG: Duration field not parsed, got %v (expected 30s)", cfg.Timeout)
	}
}

// ============================================================================
// PRESERVATION TESTS — These test EXISTING behavior (expected to PASS)
// ============================================================================

func TestPreservation_RedactCredentials(t *testing.T) {
	result := RedactCredentials("mongodb://user:pass@cluster.mongodb.net/db")
	if !strings.Contains(result, "****") {
		t.Fatalf("expected redacted output, got: %s", result)
	}
}

func TestPreservation_RedactCredentials_Empty(t *testing.T) {
	result := RedactCredentials("")
	if result != "<not set>" {
		t.Fatalf("expected '<not set>', got: %s", result)
	}
}

func TestPreservation_ResolveRegion_Default(t *testing.T) {
	os.Unsetenv("AWS_REGION")
	os.Unsetenv("AWS_DEFAULT_REGION")
	if resolveRegion() != "ap-south-1" {
		t.Fatal("expected default region ap-south-1")
	}
}

func TestPreservation_ResolveRegion_FromEnv(t *testing.T) {
	os.Setenv("AWS_REGION", "us-east-1")
	defer os.Unsetenv("AWS_REGION")
	if resolveRegion() != "us-east-1" {
		t.Fatal("expected us-east-1 from env")
	}
}

func TestPreservation_PopulateFromEnv_String(t *testing.T) {
	type Cfg struct {
		Name string `env:"TEST_NAME"`
	}
	os.Setenv("TEST_NAME", "hello")
	defer os.Unsetenv("TEST_NAME")
	cfg := &Cfg{}
	populateFromEnv(cfg)
	if cfg.Name != "hello" {
		t.Fatalf("expected 'hello', got '%s'", cfg.Name)
	}
}

func TestPreservation_PopulateFromEnv_Int(t *testing.T) {
	type Cfg struct {
		Port int `env:"TEST_PORT"`
	}
	os.Setenv("TEST_PORT", "8080")
	defer os.Unsetenv("TEST_PORT")
	cfg := &Cfg{}
	populateFromEnv(cfg)
	if cfg.Port != 8080 {
		t.Fatalf("expected 8080, got %d", cfg.Port)
	}
}
