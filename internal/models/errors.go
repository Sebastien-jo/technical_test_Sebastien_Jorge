package models

import (
	"errors"
	"fmt"
)

var (
	ErrClientNotFound  = errors.New("client not found")
	ErrPolicyNotFound  = errors.New("policy not found")
	ErrRouteNotFound   = errors.New("route not found")
	ErrInvalidLimit    = errors.New("invalid limit: must be > 0")
	ErrInvalidWindow   = errors.New("invalid window: must be > 0")
	ErrInvalidRoute    = errors.New("invalid route")
	ErrDuplicateRoute  = errors.New("duplicate route in policy")
	ErrQuotaExhausted  = errors.New("quota exhausted")
	ErrMissingClientID = errors.New("missing client_id in request")
	ErrMissingRoute    = errors.New("missing route in request")
	ErrMissingMethod   = errors.New("missing method in request")
)

type ValidationError struct {
	Field   string
	Message string
}

func (ve *ValidationError) Error() string {
	return fmt.Sprintf("validation error on field '%s': %s", ve.Field, ve.Message)
}
