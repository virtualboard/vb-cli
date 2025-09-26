package feature

import (
	"fmt"
	"sort"
	"strings"
)

var statusDirectories = map[string]string{
	"backlog":     "features/backlog",
	"in-progress": "features/in-progress",
	"blocked":     "features/blocked",
	"review":      "features/review",
	"done":        "features/done",
}

var allowedTransitions = map[string][]string{
	"backlog":     {"in-progress"},
	"in-progress": {"blocked", "review"},
	"blocked":     {"in-progress"},
	"review":      {"in-progress", "done"},
	"done":        {},
}

// DirectoryForStatus returns the relative directory for a given status.
func DirectoryForStatus(status string) string {
	return statusDirectories[strings.ToLower(status)]
}

// ValidStatuses returns the sorted list of allowed statuses.
func ValidStatuses() []string {
	keys := make([]string, 0, len(statusDirectories))
	for status := range statusDirectories {
		keys = append(keys, status)
	}
	sort.Strings(keys)
	return keys
}

// ValidateStatus ensures status exists.
func ValidateStatus(status string) error {
	status = strings.ToLower(status)
	if _, ok := statusDirectories[status]; !ok {
		return fmt.Errorf("invalid status '%s'", status)
	}
	return nil
}

// ValidateTransition ensures the transition is permitted.
func ValidateTransition(current, target string) error {
	current = strings.ToLower(current)
	target = strings.ToLower(target)

	if err := ValidateStatus(target); err != nil {
		return err
	}

	allowed, ok := allowedTransitions[current]
	if !ok {
		return fmt.Errorf("status '%s' cannot transition", current)
	}

	for _, next := range allowed {
		if next == target {
			return nil
		}
	}

	if len(allowed) == 0 {
		return fmt.Errorf("cannot transition from %s", current)
	}
	return fmt.Errorf("cannot transition from %s to %s", current, target)
}
