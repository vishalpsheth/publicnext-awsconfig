// Package middleware provides shared HTTP middleware for all PublicNext microservices.
package middleware

import (
	"net/http"

	"github.com/vishalpsheth/publicnext-awsconfig/apperror"
)

// MaxBytes returns middleware that limits request body size for POST, PUT, and PATCH methods.
// Requests with bodies exceeding maxBytes will fail when the handler attempts to read them.
// Use HandleMaxBytesError in your handler to detect and respond with a proper error envelope.
func MaxBytes(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch:
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// HandleMaxBytesError checks if an error is caused by exceeding the MaxBytesReader limit.
// If so, it writes an INVALID_INPUT error envelope and returns true.
// Otherwise it returns false and the caller should handle the error differently.
func HandleMaxBytesError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	// http.MaxBytesReader returns *http.MaxBytesError (Go 1.19+)
	if _, ok := err.(*http.MaxBytesError); ok {
		apperror.WriteError(w, apperror.ErrInvalidInput.WithMessage("request body too large"))
		return true
	}
	// Fallback: older Go versions or wrapped errors may use the string
	if err.Error() == "http: request body too large" {
		apperror.WriteError(w, apperror.ErrInvalidInput.WithMessage("request body too large"))
		return true
	}
	return false
}
