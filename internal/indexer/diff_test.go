package indexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeDiff(t *testing.T) {
	t.Run("no old data - all new", func(t *testing.T) {
		newData := &Data{
			Features: []Entry{
				{ID: "FEAT-001", Status: "backlog", Title: "Feature 1"},
				{ID: "FEAT-002", Status: "in-progress", Title: "Feature 2"},
			},
			Summary: map[string]int{"backlog": 1, "in-progress": 1},
		}

		diff := ComputeDiff(nil, newData)
		require.NotNil(t, diff)

		assert.True(t, diff.HasChanges())
		assert.Equal(t, 2, diff.Added)
		assert.Equal(t, 0, diff.Removed)
		assert.Equal(t, 0, diff.Changed)
		assert.Len(t, diff.Changes, 2)

		for _, change := range diff.Changes {
			assert.Equal(t, ChangeTypeAdded, change.Type)
		}
	})

	t.Run("no changes", func(t *testing.T) {
		data := &Data{
			Features: []Entry{
				{ID: "FEAT-001", Status: "backlog", Title: "Feature 1", Owner: "alice", Priority: "high", Complexity: "medium", Updated: "2024-01-01"},
			},
			Summary: map[string]int{"backlog": 1},
		}

		diff := ComputeDiff(data, data)
		require.NotNil(t, diff)

		assert.False(t, diff.HasChanges())
		assert.Equal(t, 0, diff.Added)
		assert.Equal(t, 0, diff.Removed)
		assert.Equal(t, 0, diff.Changed)
		assert.Len(t, diff.Changes, 0)
	})

	t.Run("feature added", func(t *testing.T) {
		oldData := &Data{
			Features: []Entry{
				{ID: "FEAT-001", Status: "backlog", Title: "Feature 1"},
			},
			Summary: map[string]int{"backlog": 1},
		}

		newData := &Data{
			Features: []Entry{
				{ID: "FEAT-001", Status: "backlog", Title: "Feature 1"},
				{ID: "FEAT-002", Status: "backlog", Title: "Feature 2"},
			},
			Summary: map[string]int{"backlog": 2},
		}

		diff := ComputeDiff(oldData, newData)
		require.NotNil(t, diff)

		assert.True(t, diff.HasChanges())
		assert.Equal(t, 1, diff.Added)
		assert.Equal(t, 0, diff.Removed)
		assert.Equal(t, 0, diff.Changed)
		assert.Len(t, diff.Changes, 1)

		assert.Equal(t, ChangeTypeAdded, diff.Changes[0].Type)
		assert.Equal(t, "FEAT-002", diff.Changes[0].FeatureID)
	})

	t.Run("feature removed", func(t *testing.T) {
		oldData := &Data{
			Features: []Entry{
				{ID: "FEAT-001", Status: "backlog", Title: "Feature 1"},
				{ID: "FEAT-002", Status: "backlog", Title: "Feature 2"},
			},
			Summary: map[string]int{"backlog": 2},
		}

		newData := &Data{
			Features: []Entry{
				{ID: "FEAT-001", Status: "backlog", Title: "Feature 1"},
			},
			Summary: map[string]int{"backlog": 1},
		}

		diff := ComputeDiff(oldData, newData)
		require.NotNil(t, diff)

		assert.True(t, diff.HasChanges())
		assert.Equal(t, 0, diff.Added)
		assert.Equal(t, 1, diff.Removed)
		assert.Equal(t, 0, diff.Changed)
		assert.Len(t, diff.Changes, 1)

		assert.Equal(t, ChangeTypeRemoved, diff.Changes[0].Type)
		assert.Equal(t, "FEAT-002", diff.Changes[0].FeatureID)
	})

	t.Run("status change", func(t *testing.T) {
		oldData := &Data{
			Features: []Entry{
				{ID: "FEAT-001", Status: "backlog", Title: "Feature 1"},
			},
			Summary: map[string]int{"backlog": 1},
		}

		newData := &Data{
			Features: []Entry{
				{ID: "FEAT-001", Status: "in-progress", Title: "Feature 1"},
			},
			Summary: map[string]int{"in-progress": 1},
		}

		diff := ComputeDiff(oldData, newData)
		require.NotNil(t, diff)

		assert.True(t, diff.HasChanges())
		assert.Equal(t, 0, diff.Added)
		assert.Equal(t, 0, diff.Removed)
		assert.Equal(t, 1, diff.Changed)
		assert.Len(t, diff.Changes, 1)

		assert.Equal(t, ChangeTypeStatusChange, diff.Changes[0].Type)
		assert.Equal(t, "FEAT-001", diff.Changes[0].FeatureID)
		assert.Equal(t, "backlog", diff.Changes[0].OldStatus)
		assert.Equal(t, "in-progress", diff.Changes[0].NewStatus)
	})

	t.Run("metadata change", func(t *testing.T) {
		oldData := &Data{
			Features: []Entry{
				{ID: "FEAT-001", Status: "backlog", Title: "Feature 1", Owner: "alice", Priority: "high"},
			},
			Summary: map[string]int{"backlog": 1},
		}

		newData := &Data{
			Features: []Entry{
				{ID: "FEAT-001", Status: "backlog", Title: "Feature 1 Updated", Owner: "bob", Priority: "high"},
			},
			Summary: map[string]int{"backlog": 1},
		}

		diff := ComputeDiff(oldData, newData)
		require.NotNil(t, diff)

		assert.True(t, diff.HasChanges())
		assert.Equal(t, 0, diff.Added)
		assert.Equal(t, 0, diff.Removed)
		assert.Equal(t, 0, diff.Changed)
		assert.Len(t, diff.Changes, 1)

		assert.Equal(t, ChangeTypeMetadataChange, diff.Changes[0].Type)
		assert.Equal(t, "FEAT-001", diff.Changes[0].FeatureID)
		assert.Contains(t, diff.Changes[0].Details, "title:")
		assert.Contains(t, diff.Changes[0].Details, "owner:")
	})

	t.Run("multiple changes", func(t *testing.T) {
		oldData := &Data{
			Features: []Entry{
				{ID: "FEAT-001", Status: "backlog", Title: "Feature 1"},
				{ID: "FEAT-002", Status: "in-progress", Title: "Feature 2"},
			},
			Summary: map[string]int{"backlog": 1, "in-progress": 1},
		}

		newData := &Data{
			Features: []Entry{
				{ID: "FEAT-001", Status: "done", Title: "Feature 1"},
				{ID: "FEAT-003", Status: "backlog", Title: "Feature 3"},
			},
			Summary: map[string]int{"done": 1, "backlog": 1},
		}

		diff := ComputeDiff(oldData, newData)
		require.NotNil(t, diff)

		assert.True(t, diff.HasChanges())
		assert.Equal(t, 1, diff.Added)   // FEAT-003
		assert.Equal(t, 1, diff.Removed) // FEAT-002
		assert.Equal(t, 1, diff.Changed) // FEAT-001 status change
		assert.Len(t, diff.Changes, 3)
	})

	t.Run("changes are sorted by ID", func(t *testing.T) {
		oldData := &Data{
			Features: []Entry{},
			Summary:  map[string]int{},
		}

		newData := &Data{
			Features: []Entry{
				{ID: "FEAT-003", Status: "backlog", Title: "Feature 3"},
				{ID: "FEAT-001", Status: "backlog", Title: "Feature 1"},
				{ID: "FEAT-002", Status: "backlog", Title: "Feature 2"},
			},
			Summary: map[string]int{"backlog": 3},
		}

		diff := ComputeDiff(oldData, newData)
		require.NotNil(t, diff)

		assert.Len(t, diff.Changes, 3)
		assert.Equal(t, "FEAT-001", diff.Changes[0].FeatureID)
		assert.Equal(t, "FEAT-002", diff.Changes[1].FeatureID)
		assert.Equal(t, "FEAT-003", diff.Changes[2].FeatureID)
	})
}

func TestDiffFormatSummary(t *testing.T) {
	t.Run("no changes", func(t *testing.T) {
		diff := &Diff{}
		assert.Equal(t, "No changes", diff.FormatSummary())
	})

	t.Run("only added", func(t *testing.T) {
		diff := &Diff{Added: 3, Changes: []Change{{Type: ChangeTypeAdded}}}
		assert.Equal(t, "3 added", diff.FormatSummary())
	})

	t.Run("only removed", func(t *testing.T) {
		diff := &Diff{Removed: 2, Changes: []Change{{Type: ChangeTypeRemoved}}}
		assert.Equal(t, "2 removed", diff.FormatSummary())
	})

	t.Run("only changed", func(t *testing.T) {
		diff := &Diff{Changed: 1, Changes: []Change{{Type: ChangeTypeStatusChange}}}
		assert.Equal(t, "1 transitioned", diff.FormatSummary())
	})

	t.Run("multiple types", func(t *testing.T) {
		diff := &Diff{
			Added:   3,
			Changed: 2,
			Removed: 1,
			Changes: []Change{{Type: ChangeTypeAdded}},
		}
		summary := diff.FormatSummary()
		assert.Contains(t, summary, "3 added")
		assert.Contains(t, summary, "2 transitioned")
		assert.Contains(t, summary, "1 removed")
	})
}

func TestDiffFormatVerbose(t *testing.T) {
	t.Run("no changes", func(t *testing.T) {
		diff := &Diff{}
		output := diff.FormatVerbose()
		assert.Contains(t, output, "No changes")
	})

	t.Run("with changes", func(t *testing.T) {
		diff := &Diff{
			Added:   1,
			Changed: 1,
			Removed: 1,
			Changes: []Change{
				{Type: ChangeTypeAdded, FeatureID: "FEAT-003", NewStatus: "backlog"},
				{Type: ChangeTypeStatusChange, FeatureID: "FEAT-001", OldStatus: "backlog", NewStatus: "done"},
				{Type: ChangeTypeRemoved, FeatureID: "FEAT-002"},
				{Type: ChangeTypeMetadataChange, FeatureID: "FEAT-004", Details: "title changed"},
			},
		}

		output := diff.FormatVerbose()
		assert.Contains(t, output, "Added:")
		assert.Contains(t, output, "FEAT-003 (backlog)")
		assert.Contains(t, output, "Status Changes:")
		assert.Contains(t, output, "FEAT-001: backlog → done")
		assert.Contains(t, output, "Metadata Changes:")
		assert.Contains(t, output, "FEAT-004: title changed")
		assert.Contains(t, output, "Removed:")
		assert.Contains(t, output, "FEAT-002")
	})
}

func TestDiffFormatVeryVerbose(t *testing.T) {
	t.Run("no changes", func(t *testing.T) {
		diff := &Diff{}
		output := diff.FormatVeryVerbose()
		assert.Contains(t, output, "No changes")
	})

	t.Run("with changes", func(t *testing.T) {
		diff := &Diff{
			Changes: []Change{
				{Type: ChangeTypeAdded, FeatureID: "FEAT-001", NewStatus: "backlog", Details: "New feature"},
				{Type: ChangeTypeRemoved, FeatureID: "FEAT-002"},
				{Type: ChangeTypeStatusChange, FeatureID: "FEAT-003", OldStatus: "backlog", NewStatus: "done"},
				{Type: ChangeTypeMetadataChange, FeatureID: "FEAT-004", Details: "title changed"},
			},
		}

		output := diff.FormatVeryVerbose()
		assert.Contains(t, output, "✓ FEAT-001 added")
		assert.Contains(t, output, "✗ FEAT-002 removed")
		assert.Contains(t, output, "↻ FEAT-003 transitioned")
		assert.Contains(t, output, "⚑ FEAT-004 metadata changed")
	})
}

func TestDetectMetadataChanges(t *testing.T) {
	t.Run("no changes", func(t *testing.T) {
		entry := Entry{
			Title:      "Feature 1",
			Owner:      "alice",
			Priority:   "high",
			Complexity: "medium",
			Labels:     []string{"tag1", "tag2"},
			Updated:    "2024-01-01",
		}

		changes := detectMetadataChanges(entry, entry)
		assert.Empty(t, changes)
	})

	t.Run("title change", func(t *testing.T) {
		old := Entry{Title: "Feature 1"}
		new := Entry{Title: "Feature 1 Updated"}

		changes := detectMetadataChanges(old, new)
		assert.Len(t, changes, 1)
		assert.Contains(t, changes[0], "title:")
	})

	t.Run("owner change", func(t *testing.T) {
		old := Entry{Owner: "alice"}
		new := Entry{Owner: "bob"}

		changes := detectMetadataChanges(old, new)
		assert.Len(t, changes, 1)
		assert.Contains(t, changes[0], "owner:")
	})

	t.Run("priority change", func(t *testing.T) {
		old := Entry{Priority: "high"}
		new := Entry{Priority: "low"}

		changes := detectMetadataChanges(old, new)
		assert.Len(t, changes, 1)
		assert.Contains(t, changes[0], "priority:")
	})

	t.Run("complexity change", func(t *testing.T) {
		old := Entry{Complexity: "high"}
		new := Entry{Complexity: "low"}

		changes := detectMetadataChanges(old, new)
		assert.Len(t, changes, 1)
		assert.Contains(t, changes[0], "complexity:")
	})

	t.Run("labels change", func(t *testing.T) {
		old := Entry{Labels: []string{"tag1", "tag2"}}
		new := Entry{Labels: []string{"tag2", "tag3"}}

		changes := detectMetadataChanges(old, new)
		assert.Len(t, changes, 1)
		assert.Contains(t, changes[0], "labels:")
	})

	t.Run("updated timestamp change", func(t *testing.T) {
		old := Entry{Updated: "2024-01-01"}
		new := Entry{Updated: "2024-01-02"}

		changes := detectMetadataChanges(old, new)
		assert.Len(t, changes, 1)
		assert.Contains(t, changes[0], "updated:")
	})

	t.Run("multiple changes", func(t *testing.T) {
		old := Entry{
			Title:    "Feature 1",
			Owner:    "alice",
			Priority: "high",
		}
		new := Entry{
			Title:    "Feature 1 Updated",
			Owner:    "bob",
			Priority: "low",
		}

		changes := detectMetadataChanges(old, new)
		assert.Len(t, changes, 3)
	})
}

func TestDiffHasChanges(t *testing.T) {
	t.Run("empty diff", func(t *testing.T) {
		diff := &Diff{}
		assert.False(t, diff.HasChanges())
	})

	t.Run("with changes", func(t *testing.T) {
		diff := &Diff{
			Changes: []Change{{Type: ChangeTypeAdded, FeatureID: "FEAT-001"}},
		}
		assert.True(t, diff.HasChanges())
	})
}
