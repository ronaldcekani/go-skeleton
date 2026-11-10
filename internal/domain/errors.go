package domain

import (
	"errors"
	"fmt"
)

// ErrKind classifies an error by what the caller can do about it. The
// transports translate kinds into HTTP statuses and gRPC codes in one place
// each, so handlers and services never deal with transport error codes.
type ErrKind int

const (
	// ErrKindInternal is the zero value: any error that is not a *Error,
	// such as a failed SQL query, is treated as internal.
	ErrKindInternal ErrKind = iota
	ErrKindInvalidInput
	ErrKindNotFound
	ErrKindConflict
	ErrKindUnauthenticated
	ErrKindPermissionDenied
	ErrKindRateLimited
)

// String returns the stable, machine-readable name of the kind. It is used
// as the error code in API responses.
func (k ErrKind) String() string {
	switch k {
	case ErrKindInvalidInput:
		return "invalid_input"
	case ErrKindNotFound:
		return "not_found"
	case ErrKindConflict:
		return "conflict"
	case ErrKindUnauthenticated:
		return "unauthenticated"
	case ErrKindPermissionDenied:
		return "permission_denied"
	case ErrKindRateLimited:
		return "rate_limited"
	default:
		return "internal"
	}
}

// Error is an error with a kind. Message is safe to show to API clients; the
// wrapped cause is not, and only ever reaches the logs.
type Error struct {
	Kind    ErrKind
	Message string
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.cause)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.cause }

// NewError returns an error of the given kind with a client-safe message.
func NewError(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// WrapError is NewError with an underlying cause kept for logging.
func WrapError(kind ErrKind, cause error, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...), cause: cause}
}

func InvalidInput(format string, args ...any) *Error {
	return NewError(ErrKindInvalidInput, format, args...)
}

func NotFound(format string, args ...any) *Error {
	return NewError(ErrKindNotFound, format, args...)
}

func Unauthenticated(format string, args ...any) *Error {
	return NewError(ErrKindUnauthenticated, format, args...)
}

func Conflict(format string, args ...any) *Error {
	return NewError(ErrKindConflict, format, args...)
}

// KindOf returns the kind of the first *Error in err's chain, or
// ErrKindInternal if there is none.
func KindOf(err error) ErrKind {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Kind
	}
	return ErrKindInternal
}

// IsKind reports whether err is of the given kind.
func IsKind(err error, kind ErrKind) bool {
	return err != nil && KindOf(err) == kind
}

// PublicMessage returns the text that may be shown to an API client: the
// message of the first *Error in err's chain, unless the error is internal,
// in which case the details stay private.
func PublicMessage(err error) string {
	if e, ok := errors.AsType[*Error](err); ok && e.Kind != ErrKindInternal {
		return e.Message
	}
	return "internal server error"
}
