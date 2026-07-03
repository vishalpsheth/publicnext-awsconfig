package middleware

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// traceLogKey is the context key for the trace-enriched logger.
type traceLogKey struct{}

// TraceLogCorrelation returns middleware that injects OTel trace_id and span_id
// into the request-scoped logger. Downstream handlers and middleware can retrieve
// the enriched logger via LoggerFromContext.
//
// If the request has no valid OTel span context, the base logger is used unchanged.
func TraceLogCorrelation(base *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			spanCtx := trace.SpanFromContext(r.Context()).SpanContext()
			if spanCtx.IsValid() {
				enriched := base.With(
					zap.String("trace_id", spanCtx.TraceID().String()),
					zap.String("span_id", spanCtx.SpanID().String()),
				)
				ctx := context.WithValue(r.Context(), traceLogKey{}, enriched)
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// LoggerFromContext retrieves the trace-enriched logger from the request context.
// If no enriched logger is present (e.g., no active span), the provided base logger
// is returned unchanged.
func LoggerFromContext(ctx context.Context, base *zap.Logger) *zap.Logger {
	if l, ok := ctx.Value(traceLogKey{}).(*zap.Logger); ok {
		return l
	}
	return base
}
