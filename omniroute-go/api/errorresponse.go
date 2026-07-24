package api

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
)

// errorresponse.go — Standardized API error responses.
// Port of src/lib/api/errorResponse.ts
//
// Pure functions — no race concerns.

// ApiErrorType is the machine-readable error category.
type ApiErrorType string

const (
	ErrInvalidRequest ApiErrorType = "invalid_request"
	ErrNotFound       ApiErrorType = "not_found"
	ErrConflict       ApiErrorType = "conflict"
	ErrServerError    ApiErrorType = "server_error"
)

// ApiErrorPayload is the input to CreateErrorResponse.
type ApiErrorPayload struct {
	Status  int
	Message string
	Type    ApiErrorType
	Details any
}

// ApiErrorBody is the JSON error envelope.
type ApiErrorBody struct {
	Error struct {
		Message string       `json:"message"`
		Type    ApiErrorType `json:"type"`
		Details any          `json:"details,omitempty"`
	} `json:"error"`
	RequestID string `json:"requestId"`
}

// newRequestID generates a random UUID v4.
func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// resolveErrorType derives the error type from status when not provided.
func resolveErrorType(payload ApiErrorPayload) ApiErrorType {
	if payload.Type != "" {
		return payload.Type
	}
	switch {
	case payload.Status >= 500:
		return ErrServerError
	case payload.Status == 404:
		return ErrNotFound
	case payload.Status == 409:
		return ErrConflict
	default:
		return ErrInvalidRequest
	}
}

// CreateErrorResponse builds the error envelope and HTTP status. Returns the
// JSON body and the resolved status code.
func CreateErrorResponse(payload ApiErrorPayload) (body []byte, status int) {
	var out ApiErrorBody
	out.Error.Message = payload.Message
	out.Error.Type = resolveErrorType(payload)
	out.Error.Details = payload.Details
	out.RequestID = newRequestID()
	data, _ := json.Marshal(out)
	return data, payload.Status
}

// UnknownError is the shape accepted by CreateErrorResponseFromUnknown.
type UnknownError struct {
	Message string
	Status  int
	Type    ApiErrorType
	Details any
}

// CreateErrorResponseFromUnknown builds an error response from an arbitrary
// error, defaulting to 500 / fallbackMessage.
func CreateErrorResponseFromUnknown(err *UnknownError, fallbackMessage string) (body []byte, status int) {
	if fallbackMessage == "" {
		fallbackMessage = "Unexpected server error"
	}
	status = 500
	message := fallbackMessage
	var errType ApiErrorType
	var details any
	if err != nil {
		if err.Status != 0 {
			status = err.Status
		}
		if err.Message != "" {
			message = err.Message
		}
		errType = err.Type
		details = err.Details
	}
	return CreateErrorResponse(ApiErrorPayload{
		Status:  status,
		Message: message,
		Type:    errType,
		Details: details,
	})
}
