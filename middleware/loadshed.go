package middleware

import (
	"net/http"
	"runtime"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/vishalpsheth/publicnext-awsconfig/apperror"
)

// LoadShedConfig configures the adaptive load shedding middleware.
// HighWatermark: goroutine count at which shedding begins.
// LowWatermark: goroutine count at which shedding stops (must be < HighWatermark).
type LoadShedConfig struct {
	HighWatermark int
	LowWatermark  int
}

// LoadShedding returns middleware that rejects requests with 503 when the service
// is saturated beyond the configured goroutine threshold. Uses hysteresis to prevent
// oscillation: starts shedding at HighWatermark, stops at LowWatermark.
//
// The normal path (not shedding) performs a single atomic read — zero allocations,
// negligible latency overhead.
func LoadShedding(cfg LoadShedConfig, registerer prometheus.Registerer) func(http.Handler) http.Handler {
	var shedding atomic.Bool

	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "load_shedding_active",
		Help: "Whether load shedding is currently enabled (1) or disabled (0)",
	})
	if registerer != nil {
		registerer.MustRegister(gauge)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			numGoroutines := runtime.NumGoroutine()

			// Hysteresis: transition UP at high watermark, DOWN at low watermark.
			// Between the two thresholds, maintain current state.
			if numGoroutines >= cfg.HighWatermark {
				if !shedding.Load() {
					shedding.Store(true)
					gauge.Set(1)
				}
			} else if numGoroutines <= cfg.LowWatermark {
				if shedding.Load() {
					shedding.Store(false)
					gauge.Set(0)
				}
			}

			if shedding.Load() {
				w.Header().Set("Retry-After", "5")
				apperror.WriteError(w, apperror.ErrCircuitOpen)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
