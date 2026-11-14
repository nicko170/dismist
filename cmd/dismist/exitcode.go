package main

import (
	"errors"
	"fmt"
)

// Exit codes of `dismist ap33 onboard` and `restore` (design §3.15). Any
// other failure, including a usage error, exits 2 like a failed preflight.
const (
	exitDone          = 0
	exitPreflight     = 2
	exitTLSMismatch   = 3
	exitAPIError      = 4
	exitTimedOut      = 5
	exitLocalFilesSet = 6
)

// exitError carries a process exit code with the error that caused it.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }

func (e *exitError) Unwrap() error { return e.err }

func withExit(code int, err error) error {
	if err == nil {
		return nil
	}
	var existing *exitError
	if errors.As(err, &existing) {
		return err
	}
	return &exitError{code: code, err: err}
}

func exitf(code int, format string, args ...any) error {
	return &exitError{code: code, err: fmt.Errorf(format, args...)}
}

// exitCode is the process exit status for err.
func exitCode(err error) int {
	if err == nil {
		return exitDone
	}
	var coded *exitError
	if errors.As(err, &coded) {
		return coded.code
	}
	return exitPreflight
}
