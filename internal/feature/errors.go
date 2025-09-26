package feature

import "errors"

var (
	// ErrNotFound indicates the requested feature does not exist.
	ErrNotFound = errors.New("feature not found")
	// ErrInvalidTransition indicates a status transition is not permitted.
	ErrInvalidTransition = errors.New("invalid status transition")
	// ErrDependencyBlocked indicates dependencies are incomplete.
	ErrDependencyBlocked = errors.New("dependency not satisfied")
)
