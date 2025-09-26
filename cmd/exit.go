package cmd

import "fmt"

// Exit codes as defined in the spec.
const (
	ExitCodeSuccess           = 0
	ExitCodeValidation        = 1
	ExitCodeNotFound          = 2
	ExitCodeInvalidTransition = 3
	ExitCodeDependency        = 4
	ExitCodeLockConflict      = 5
	ExitCodeFilesystem        = 6
	ExitCodeSchema            = 7
	ExitCodeUnknown           = 10
)

// CLIError allows returning rich errors with exit codes.
type CLIError struct {
	Code int
	Err  error
}

func (e *CLIError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit code %d", e.Code)
	}
	return e.Err.Error()
}

func (e *CLIError) Unwrap() error {
	return e.Err
}

// NewCLIError constructs a CLIError with a message and exit code.
func NewCLIError(code int, msg string) error {
	return &CLIError{Code: code, Err: fmt.Errorf("%s", msg)}
}

// WrapCLIError converts any error into a CLIError with the provided code.
func WrapCLIError(code int, err error) error {
	if err == nil {
		return nil
	}
	return &CLIError{Code: code, Err: err}
}

// ExitCode extracts an exit code from an error, returning 1 if not specified.
func ExitCode(err error) int {
	if err == nil {
		return ExitCodeSuccess
	}
	if cliErr, ok := err.(*CLIError); ok {
		if cliErr.Code == 0 {
			return ExitCodeUnknown
		}
		return cliErr.Code
	}
	return ExitCodeUnknown
}
