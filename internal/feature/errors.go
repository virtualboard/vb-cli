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
	// ErrDependencyCycle indicates a proposed feature graph contains a cycle.
	ErrDependencyCycle = errors.New("feature dependency cycle")
	// ErrDeleteConflict indicates a feature cannot be safely deleted under the
	// fixed backlog/unreferenced deletion policy.
	ErrDeleteConflict = errors.New("feature cannot be deleted")
	// ErrOwnershipConflict indicates the actor does not own the feature.
	ErrOwnershipConflict = errors.New("feature is owned by another actor")
	// ErrLockConflict indicates another actor owns an active feature lock.
	ErrLockConflict = errors.New("feature has an active lock owned by another actor")
	// ErrDuplicateID indicates multiple files claim the same immutable feature ID.
	ErrDuplicateID = errors.New("duplicate feature ID")
	// ErrIdentityMismatch indicates immutable filename and frontmatter identities disagree.
	ErrIdentityMismatch = errors.New("feature identity invariant violated")
	// ErrStaleFeature indicates that the on-disk source changed after it was read.
	ErrStaleFeature = errors.New("feature changed during mutation")
	// ErrStatusReadiness indicates that required body evidence is incomplete for
	// a review or done transition.
	ErrStatusReadiness = errors.New("feature is not ready for the target status")
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
