package indexer

import (
	"fmt"
	"sort"
	"strings"
)

// ChangeType represents the type of change detected
type ChangeType string

const (
	ChangeTypeAdded          ChangeType = "added"
	ChangeTypeRemoved        ChangeType = "removed"
	ChangeTypeStatusChange   ChangeType = "status_change"
	ChangeTypeMetadataChange ChangeType = "metadata_change"
)

// Change represents a detected change in the index
type Change struct {
	Type      ChangeType
	FeatureID string
	OldStatus string
	NewStatus string
	Details   string // Human-readable description of the change
}

// Diff represents the differences between two index states
type Diff struct {
	Changes []Change
	Added   int
	Removed int
	Changed int
}

// HasChanges returns true if there are any changes detected
func (d *Diff) HasChanges() bool {
	return len(d.Changes) > 0
}

// FormatSummary returns a concise summary of changes
func (d *Diff) FormatSummary() string {
	if !d.HasChanges() {
		return "No changes"
	}

	parts := []string{}
	if d.Added > 0 {
		parts = append(parts, fmt.Sprintf("%d added", d.Added))
	}
	if d.Changed > 0 {
		parts = append(parts, fmt.Sprintf("%d transitioned", d.Changed))
	}
	if d.Removed > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", d.Removed))
	}

	return strings.Join(parts, ", ")
}

// FormatVerbose returns a detailed list of changes with feature IDs
func (d *Diff) FormatVerbose() string {
	if !d.HasChanges() {
		return "No changes detected"
	}

	var b strings.Builder

	// Group changes by type
	added := []Change{}
	removed := []Change{}
	statusChanges := []Change{}
	metadataChanges := []Change{}

	for _, change := range d.Changes {
		switch change.Type {
		case ChangeTypeAdded:
			added = append(added, change)
		case ChangeTypeRemoved:
			removed = append(removed, change)
		case ChangeTypeStatusChange:
			statusChanges = append(statusChanges, change)
		case ChangeTypeMetadataChange:
			metadataChanges = append(metadataChanges, change)
		}
	}

	// Format each section
	if len(added) > 0 {
		b.WriteString("Added:\n")
		for _, change := range added {
			b.WriteString(fmt.Sprintf("  • %s (%s)\n", change.FeatureID, change.NewStatus))
		}
		b.WriteString("\n")
	}

	if len(statusChanges) > 0 {
		b.WriteString("Status Changes:\n")
		for _, change := range statusChanges {
			b.WriteString(fmt.Sprintf("  • %s: %s → %s\n", change.FeatureID, change.OldStatus, change.NewStatus))
		}
		b.WriteString("\n")
	}

	if len(metadataChanges) > 0 {
		b.WriteString("Metadata Changes:\n")
		for _, change := range metadataChanges {
			b.WriteString(fmt.Sprintf("  • %s: %s\n", change.FeatureID, change.Details))
		}
		b.WriteString("\n")
	}

	if len(removed) > 0 {
		b.WriteString("Removed:\n")
		for _, change := range removed {
			b.WriteString(fmt.Sprintf("  • %s\n", change.FeatureID))
		}
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

// FormatVeryVerbose returns a detailed description of all changes
func (d *Diff) FormatVeryVerbose() string {
	if !d.HasChanges() {
		return "No changes detected"
	}

	var b strings.Builder

	for i, change := range d.Changes {
		if i > 0 {
			b.WriteString("\n")
		}

		switch change.Type {
		case ChangeTypeAdded:
			b.WriteString(fmt.Sprintf("✓ %s added (status: %s)\n", change.FeatureID, change.NewStatus))
			if change.Details != "" {
				b.WriteString(fmt.Sprintf("  %s\n", change.Details))
			}

		case ChangeTypeRemoved:
			b.WriteString(fmt.Sprintf("✗ %s removed\n", change.FeatureID))

		case ChangeTypeStatusChange:
			b.WriteString(fmt.Sprintf("↻ %s transitioned: %s → %s\n", change.FeatureID, change.OldStatus, change.NewStatus))

		case ChangeTypeMetadataChange:
			b.WriteString(fmt.Sprintf("⚑ %s metadata changed\n", change.FeatureID))
			b.WriteString(fmt.Sprintf("  %s\n", change.Details))
		}
	}

	return b.String()
}

// ComputeDiff compares old and new index data to detect changes
func ComputeDiff(oldData, newData *Data) *Diff {
	diff := &Diff{
		Changes: []Change{},
	}

	if oldData == nil {
		// No old data, everything is new
		for _, entry := range newData.Features {
			diff.Changes = append(diff.Changes, Change{
				Type:      ChangeTypeAdded,
				FeatureID: entry.ID,
				NewStatus: entry.Status,
			})
			diff.Added++
		}
		return diff
	}

	// Build lookup maps
	oldMap := make(map[string]Entry)
	for _, entry := range oldData.Features {
		oldMap[entry.ID] = entry
	}

	newMap := make(map[string]Entry)
	for _, entry := range newData.Features {
		newMap[entry.ID] = entry
	}

	// Detect added and changed features
	for _, newEntry := range newData.Features {
		oldEntry, existed := oldMap[newEntry.ID]

		if !existed {
			// New feature added
			diff.Changes = append(diff.Changes, Change{
				Type:      ChangeTypeAdded,
				FeatureID: newEntry.ID,
				NewStatus: newEntry.Status,
			})
			diff.Added++
			continue
		}

		// Check for status change
		if oldEntry.Status != newEntry.Status {
			diff.Changes = append(diff.Changes, Change{
				Type:      ChangeTypeStatusChange,
				FeatureID: newEntry.ID,
				OldStatus: oldEntry.Status,
				NewStatus: newEntry.Status,
			})
			diff.Changed++
			continue
		}

		// Check for other metadata changes
		metadataChanges := detectMetadataChanges(oldEntry, newEntry)
		if len(metadataChanges) > 0 {
			diff.Changes = append(diff.Changes, Change{
				Type:      ChangeTypeMetadataChange,
				FeatureID: newEntry.ID,
				Details:   strings.Join(metadataChanges, ", "),
			})
		}
	}

	// Detect removed features
	for _, oldEntry := range oldData.Features {
		if _, exists := newMap[oldEntry.ID]; !exists {
			diff.Changes = append(diff.Changes, Change{
				Type:      ChangeTypeRemoved,
				FeatureID: oldEntry.ID,
				OldStatus: oldEntry.Status,
			})
			diff.Removed++
		}
	}

	// Sort changes by feature ID for deterministic output
	sort.Slice(diff.Changes, func(i, j int) bool {
		return diff.Changes[i].FeatureID < diff.Changes[j].FeatureID
	})

	return diff
}

// detectMetadataChanges compares two entries and returns a list of changed fields
func detectMetadataChanges(old, new Entry) []string {
	changes := []string{}

	if old.Title != new.Title {
		changes = append(changes, fmt.Sprintf("title: %q → %q", old.Title, new.Title))
	}

	if old.Owner != new.Owner {
		changes = append(changes, fmt.Sprintf("owner: %q → %q", old.Owner, new.Owner))
	}

	if old.Priority != new.Priority {
		changes = append(changes, fmt.Sprintf("priority: %s → %s", old.Priority, new.Priority))
	}

	if old.Complexity != new.Complexity {
		changes = append(changes, fmt.Sprintf("complexity: %s → %s", old.Complexity, new.Complexity))
	}

	// Compare labels
	oldLabels := strings.Join(old.Labels, ", ")
	newLabels := strings.Join(new.Labels, ", ")
	if oldLabels != newLabels {
		changes = append(changes, fmt.Sprintf("labels: [%s] → [%s]", oldLabels, newLabels))
	}

	if old.Updated != new.Updated {
		changes = append(changes, fmt.Sprintf("updated: %s → %s", old.Updated, new.Updated))
	}

	return changes
}
