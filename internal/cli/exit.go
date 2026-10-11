package cli

import (
	"errors"
	"fmt"

	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/manifest"
)

// Exit codes (contracts/cli.md).
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
	ExitTool    = 3
)

// ExitError carries an explicit exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error { return e.Err }

// withCode wraps err with an exit code; nil stays nil.
func withCode(code int, err error) error {
	if err == nil {
		return nil
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return err
	}
	return &ExitError{Code: code, Err: err}
}

// usageErr is a validation or usage error (exit 2).
func usageErr(format string, args ...any) error {
	return &ExitError{Code: ExitUsage, Err: fmt.Errorf(format, args...)}
}

// toolErr is a missing or wrong external tool (exit 3).
func toolErr(err error) error { return withCode(ExitTool, err) }

// silentErr exits with code without printing anything more.
type silentErr struct{ code int }

func (s silentErr) Error() string { return fmt.Sprintf("exit status %d", s.code) }

// CodeOf maps an error to its exit code.
func CodeOf(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	var se silentErr
	if errors.As(err, &se) {
		return se.code
	}
	var ve *manifest.ValidationError
	if errors.As(err, &ve) || errors.Is(err, manifest.ErrUnsupportedVersion) {
		return ExitUsage
	}
	if errors.Is(err, engine.ErrNoEngine) {
		return ExitTool
	}
	return ExitFailure
}
