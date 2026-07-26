// Package response is the single source of truth for the API envelope.
// Every handler answers through these helpers so the client always sees the
// same shape: {success, data, error{code,message,fields}}.
package response

import (
	"encoding/json"
	"net/http"
)

// Envelope is the uniform JSON body returned by every endpoint.
type Envelope struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

// Error carries a machine code, a human message, and optional per-field
// validation errors. `Fields` mirrors the frontend `error.fields` shape so
// client and server speak one error language (see lib/validators.ts).
type Error struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// Common error codes. Keep these stable — the client branches on them.
const (
	CodeValidation   = "validation_error"
	CodeUnauthorized = "unauthorized"
	CodeForbidden    = "forbidden"
	CodeNotFound     = "not_found"
	CodeConflict     = "conflict"
	CodeRateLimited  = "rate_limited"
	CodeInternal     = "internal_error"
	CodeNotImpl      = "not_implemented"
)

// JSON writes an arbitrary payload with the given status code.
func JSON(w http.ResponseWriter, status int, env Envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(env)
}

// OK writes a 200 success envelope.
func OK(w http.ResponseWriter, data any) {
	JSON(w, http.StatusOK, Envelope{Success: true, Data: data})
}

// Created writes a 201 success envelope.
func Created(w http.ResponseWriter, data any) {
	JSON(w, http.StatusCreated, Envelope{Success: true, Data: data})
}

// Fail writes an error envelope with the given status and code.
func Fail(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, Envelope{Success: false, Error: &Error{Code: code, Message: message}})
}

// FailFields writes a validation error envelope carrying per-field messages.
func FailFields(w http.ResponseWriter, fields map[string]string) {
	JSON(w, http.StatusUnprocessableEntity, Envelope{
		Success: false,
		Error:   &Error{Code: CodeValidation, Message: "Validation failed", Fields: fields},
	})
}

// NotImplemented is a placeholder for routes scaffolded but not yet built.
func NotImplemented(w http.ResponseWriter) {
	Fail(w, http.StatusNotImplemented, CodeNotImpl, "Not implemented yet")
}
