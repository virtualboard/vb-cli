package feature

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNotFound indicates the requested feature does not exist.
	ErrNotFound = errors.New("feature not found")
	// ErrInvalidTransition indicates a status transition is not permitted.
	ErrInvalidTransition = errors.New("invalid status transition")
	// ErrDependencyBlocked indicates dependencies are incomplete.
	ErrDependencyBlocked = errors.New("dependency not satisfied")
)

// InvalidFileError represents one or more markdown files that failed to parse as feature specs.
type InvalidFileError struct {
	Files []InvalidFile
}

// InvalidFile holds information about a single file that failed validation.
type InvalidFile struct {
	Path   string
	Reason string
}

// Error implements the error interface.
func (e *InvalidFileError) Error() string {
	if len(e.Files) == 0 {
		return "no invalid files"
	}
	if len(e.Files) == 1 {
		return fmt.Sprintf("invalid feature file: %s: %s", e.Files[0].Path, e.Files[0].Reason)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("found %d invalid feature files:\n", len(e.Files)))
	for _, f := range e.Files {
		sb.WriteString(fmt.Sprintf("  - %s: %s\n", f.Path, f.Reason))
	}
	sb.WriteString("\nThese files do not follow the feature spec format. Please review and move them to another directory if they are not feature specs.")
	return sb.String()
}
