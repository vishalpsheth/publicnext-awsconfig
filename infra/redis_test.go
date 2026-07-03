package infra

import (
	"testing"
)

// ============================================================================
// EXPLORATION TESTS — These test BUG CONDITIONS (expected to FAIL on unfixed code)
// ============================================================================

// Test 1d: Redis TLS not applied for non-private IP (bug)
func TestExploration_RedisTLSNotAppliedForPublicIP(t *testing.T) {
	opts, err := getRedisOptions("52.66.10.5:6379")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.TLSConfig == nil {
		t.Fatal("BUG: TLS not applied for non-private IP address 52.66.10.5:6379")
	}
}

// ============================================================================
// PRESERVATION TESTS — These test EXISTING behavior (expected to PASS)
// ============================================================================

func TestPreservation_RedisURL_TLS(t *testing.T) {
	opts, err := getRedisOptions("rediss://host:6380")
	if err != nil {
		t.Fatal(err)
	}
	if opts.TLSConfig == nil {
		t.Fatal("expected TLS for rediss://")
	}
}

func TestPreservation_RedisEmpty_Error(t *testing.T) {
	_, err := getRedisOptions("")
	if err == nil {
		t.Fatal("expected error for empty addr")
	}
}

func TestPreservation_RedisPrivateIP_NoTLS(t *testing.T) {
	opts, err := getRedisOptions("10.0.10.19:6379")
	if err != nil {
		t.Fatal(err)
	}
	// Currently no TLS for plain host:port (preservation: private IPs stay no-TLS after fix too)
	if opts.TLSConfig != nil {
		t.Fatal("private IP should not have TLS")
	}
}
