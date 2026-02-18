package pcerrors

import (
	"context"
	"errors"
	"fmt"

	assert "github.com/ZanzyTHEbar/assert-lib"
	errbuilder "github.com/ZanzyTHEbar/errbuilder-go"
)

// Code is the canonical PicoClaw error code type.
//
// We intentionally re-export errbuilder's gRPC-inspired code set so callers can
// classify errors without inventing ad-hoc sentinels.
type Code = errbuilder.ErrCode

const (
	CodeCanceled           = errbuilder.CodeCanceled
	CodeUnknown            = errbuilder.CodeUnknown
	CodeInvalidArgument    = errbuilder.CodeInvalidArgument
	CodeDeadlineExceeded   = errbuilder.CodeDeadlineExceeded
	CodeNotFound           = errbuilder.CodeNotFound
	CodeAlreadyExists      = errbuilder.CodeAlreadyExists
	CodePermissionDenied   = errbuilder.CodePermissionDenied
	CodeResourceExhausted  = errbuilder.CodeResourceExhausted
	CodeFailedPrecondition = errbuilder.CodeFailedPrecondition
	CodeAborted            = errbuilder.CodeAborted
	CodeOutOfRange         = errbuilder.CodeOutOfRange
	CodeUnimplemented      = errbuilder.CodeUnimplemented
	CodeInternal           = errbuilder.CodeInternal
	CodeUnavailable        = errbuilder.CodeUnavailable
	CodeDataLoss           = errbuilder.CodeDataLoss
	CodeUnauthenticated    = errbuilder.CodeUnauthenticated
)

// ErrMap is a key->error bag for validation-style error details.
type ErrMap = errbuilder.ErrorMap

// Option configures an ErrBuilder before it is returned as an error.
type Option func(*buildOptions)

type buildOptions struct {
	label   string
	cause   error
	details ErrMap
}

func WithLabel(label string) Option {
	return func(o *buildOptions) { o.label = label }
}

func WithCause(err error) Option {
	return func(o *buildOptions) { o.cause = err }
}

// WithDetail sets a single key/value detail.
//
// msg must be a string or error; other types panic (this matches errbuilder.ErrorMap.Set).
func WithDetail(key string, msg any) Option {
	return func(o *buildOptions) {
		o.details.Set(key, msg)
	}
}

// WithDetails merges an entire error map into the error details.
func WithDetails(m ErrMap) Option {
	return func(o *buildOptions) {
		if m == nil {
			return
		}
		if o.details == nil {
			o.details = make(ErrMap, len(m))
		}
		for k, v := range m {
			o.details[k] = v
		}
	}
}

// New constructs a structured PicoClaw error.
//
// This returns an *errbuilder.ErrBuilder which:
// - implements error
// - supports Unwrap() so stdlib errors.Is/errors.As continue to work
// - carries a Code for classification.
func New(code Code, msg string, opts ...Option) error {
	o := buildOptions{}
	for _, opt := range opts {
		opt(&o)
	}

	b := errbuilder.New().WithCode(code).WithMsg(msg)
	if o.label != "" {
		b = b.WithLabel(o.label)
	}
	if o.cause != nil {
		// Ensure context cancellation/deadline get codes if they weren't wrapped already.
		b = b.WithCause(errbuilder.WrapIfContextError(o.cause))
	}
	if o.details != nil {
		b = b.WithDetails(errbuilder.NewErrDetails(o.details))
	}
	return b
}

// Newf is like New but formats the message via fmt.Sprintf.
//
// Callers use this instead of fmt.Errorf when they want formatting
// without creating a second wrapping error layer.
func Newf(code Code, format string, args ...any) error {
	return New(code, fmt.Sprintf(format, args...))
}

// Wrap wraps err with a new structured error code and message.
func Wrap(code Code, err error, msg string, opts ...Option) error {
	if err == nil {
		return nil
	}
	return New(code, msg, append(opts, WithCause(err))...)
}

// Wrapf wraps err with a formatted message.
func Wrapf(code Code, err error, format string, args ...any) error {
	return Wrap(code, err, fmt.Sprintf(format, args...))
}

// CodeOf returns the structured code for err if it is (or wraps) an ErrBuilder.
// Otherwise it returns CodeUnknown.
func CodeOf(err error) Code {
	return errbuilder.CodeOf(err)
}

// Is is a thin wrapper over errors.Is.
func Is(err error, target error) bool {
	return errors.Is(err, target)
}

// As is a type-safe wrapper over errors.As.
//
// Go's errors.As will panic if the provided target is not a pointer to an
// interface or a type implementing error.
//
// By constraining T to error, we eliminate the most common foot-gun at compile time.
func As[T error](err error) (T, bool) {
	var target T
	if err == nil {
		return target, false
	}
	if errors.As(err, &target) {
		return target, true
	}
	return target, false
}

// AsInto mirrors errors.As but keeps the type safety of T error.
func AsInto[T error](err error, target *T) bool {
	if err == nil || target == nil {
		return false
	}
	return errors.As(err, target)
}

// AsErrBuilder extracts an underlying *errbuilder.ErrBuilder if present.
func AsErrBuilder(err error) (*errbuilder.ErrBuilder, bool) {
	return As[*errbuilder.ErrBuilder](err)
}

// Assert integrates assert-lib so callers can opt into lightweight runtime checks.
//
// We default to production-friendly formatting, and we keep the library's safe-by-default behavior.
func Assert(ctx context.Context, condition bool, msg string, opts ...assert.Option) {
	assert.Assert(ctx, condition, msg, append([]assert.Option{assert.WithProductionDefaults()}, opts...)...)
}
