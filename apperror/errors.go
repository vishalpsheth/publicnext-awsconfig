// Package apperror provides standardized error types and response formatting
// for all PublicNext microservices. All API error responses use the envelope
// format: {"error": {"code": "...", "message": "..."}}
package apperror

import (
	"encoding/json"
	"net/http"
)

// AppError represents a structured API error with machine-readable code,
// human-readable message, and HTTP status code.
type AppError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

// Error implements the error interface.
func (e *AppError) Error() string { return e.Message }

// New creates a new AppError with the given code, message, and HTTP status.
func New(code, message string, status int) *AppError {
	return &AppError{Code: code, Message: message, Status: status}
}

// WithMessage creates a copy of the error with a custom message.
func (e *AppError) WithMessage(msg string) *AppError {
	return &AppError{Code: e.Code, Message: msg, Status: e.Status}
}

// Standard error codes used across all PublicNext services.
var (
	ErrNotFound     = &AppError{Code: "NOT_FOUND", Message: "resource not found", Status: 404}
	ErrInvalidInput = &AppError{Code: "INVALID_INPUT", Message: "invalid input", Status: 400}
	ErrRateLimited  = &AppError{Code: "RATE_LIMITED", Message: "too many requests", Status: 429}
	ErrUserBlocked  = &AppError{Code: "USER_BLOCKED", Message: "access denied", Status: 403}
	ErrInternal     = &AppError{Code: "INTERNAL_ERROR", Message: "internal error", Status: 500}
	ErrCircuitOpen  = &AppError{Code: "CIRCUIT_OPEN", Message: "service temporarily unavailable", Status: 503}
	ErrTimeout      = &AppError{Code: "TIMEOUT", Message: "request timeout", Status: 504}
	ErrUnauthorized = &AppError{Code: "UNAUTHORIZED", Message: "authentication required", Status: 401}
	ErrForbidden    = &AppError{Code: "FORBIDDEN", Message: "access forbidden", Status: 403}
)

// WriteError writes an AppError as a JSON error envelope to the response writer.
// Format: {"error": {"code": "...", "message": "..."}}
func WriteError(w http.ResponseWriter, err *AppError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(err.Status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"code":    err.Code,
			"message": err.Message,
		},
	})
}
