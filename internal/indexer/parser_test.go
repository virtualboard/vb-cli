package indexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMarkdown(t *testing.T) {
	t.Run("parse valid index", func(t *testing.T) {
		content := `# Features Index

> Auto-generated on 2024-01-01 - Do not edit manually

| ID | Title | Status | Owner | P | C | Labels | Updated | File |
|---|---|---|---|---|---|---|---|---|
| FEAT-001 | Test Feature | backlog | alice | high | medium | tag1, tag2 | 2024-01-01 | [backlog/FEAT-001-test.md](../features/backlog/FEAT-001-test.md) |
| FEAT-002 | Another Feature | in-progress | bob | low | high | tag3 | 2024-01-02 | [in-progress/FEAT-002-another.md](../features/in-progress/FEAT-002-another.md) |

## Summary

- **backlog**: 1
- **in-progress**: 1

**Total**: 2 features
`

		data, err := ParseMarkdown(content)
		require.NoError(t, err)
		require.NotNil(t, data)

		assert.Len(t, data.Features, 2)

		// Check first feature
		feat1 := data.Features[0]
		assert.Equal(t, "FEAT-001", feat1.ID)
		assert.Equal(t, "Test Feature", feat1.Title)
		assert.Equal(t, "backlog", feat1.Status)
		assert.Equal(t, "alice", feat1.Owner)
		assert.Equal(t, "high", feat1.Priority)
		assert.Equal(t, "medium", feat1.Complexity)
		assert.Equal(t, []string{"tag1", "tag2"}, feat1.Labels)
		assert.Equal(t, "2024-01-01", feat1.Updated)
		assert.Equal(t, "backlog/FEAT-001-test.md", feat1.Path)

		// Check second feature
		feat2 := data.Features[1]
		assert.Equal(t, "FEAT-002", feat2.ID)
		assert.Equal(t, "Another Feature", feat2.Title)
		assert.Equal(t, "in-progress", feat2.Status)
		assert.Equal(t, "bob", feat2.Owner)
		assert.Equal(t, "low", feat2.Priority)
		assert.Equal(t, "high", feat2.Complexity)
		assert.Equal(t, []string{"tag3"}, feat2.Labels)
		assert.Equal(t, "2024-01-02", feat2.Updated)

		// Check summary
		assert.Equal(t, 1, data.Summary["backlog"])
		assert.Equal(t, 1, data.Summary["in-progress"])
	})

	t.Run("parse empty index", func(t *testing.T) {
		content := `# Features Index

> Auto-generated on 2024-01-01 - Do not edit manually

| ID | Title | Status | Owner | P | C | Labels | Updated | File |
|---|---|---|---|---|---|---|---|---|

## Summary

**Total**: 0 features
`

		data, err := ParseMarkdown(content)
		require.NoError(t, err)
		require.NotNil(t, data)

		assert.Len(t, data.Features, 0)
		assert.Len(t, data.Summary, 0)
	})

	t.Run("parse index with no labels", func(t *testing.T) {
		content := `# Features Index

> Auto-generated on 2024-01-01 - Do not edit manually

| ID | Title | Status | Owner | P | C | Labels | Updated | File |
|---|---|---|---|---|---|---|---|---|
| FEAT-001 | Test Feature | backlog | alice | high | medium |  | 2024-01-01 | [backlog/FEAT-001-test.md](../features/backlog/FEAT-001-test.md) |

## Summary

- **backlog**: 1

**Total**: 1 features
`

		data, err := ParseMarkdown(content)
		require.NoError(t, err)
		require.NotNil(t, data)

		assert.Len(t, data.Features, 1)
		feat := data.Features[0]
		assert.Empty(t, feat.Labels)
	})

	t.Run("parse index with malformed rows", func(t *testing.T) {
		content := `# Features Index

> Auto-generated on 2024-01-01 - Do not edit manually

| ID | Title | Status | Owner | P | C | Labels | Updated | File |
|---|---|---|---|---|---|---|---|---|
| FEAT-001 | Test Feature | backlog | alice | high | medium | tag1 | 2024-01-01 | [backlog/FEAT-001-test.md](../features/backlog/FEAT-001-test.md) |
| INVALID | TOO | FEW |
| FEAT-002 | Another Feature | done | bob | low | high | tag2 | 2024-01-02 | [done/FEAT-002-another.md](../features/done/FEAT-002-another.md) |

## Summary

- **backlog**: 1
- **done**: 1

**Total**: 2 features
`

		data, err := ParseMarkdown(content)
		require.NoError(t, err)
		require.NotNil(t, data)

		// Should skip malformed row and parse valid ones
		assert.Len(t, data.Features, 2)
		assert.Equal(t, "FEAT-001", data.Features[0].ID)
		assert.Equal(t, "FEAT-002", data.Features[1].ID)
	})

	t.Run("parse empty string", func(t *testing.T) {
		data, err := ParseMarkdown("")
		require.NoError(t, err)
		require.NotNil(t, data)
		assert.Len(t, data.Features, 0)
	})
}

func TestParseTableRow(t *testing.T) {
	t.Run("parse valid row with link", func(t *testing.T) {
		row := "| FEAT-001 | Test Feature | backlog | alice | high | medium | tag1, tag2 | 2024-01-01 | [backlog/FEAT-001-test.md](../features/backlog/FEAT-001-test.md) |"

		entry, err := parseTableRow(row)
		require.NoError(t, err)

		assert.Equal(t, "FEAT-001", entry.ID)
		assert.Equal(t, "Test Feature", entry.Title)
		assert.Equal(t, "backlog", entry.Status)
		assert.Equal(t, "alice", entry.Owner)
		assert.Equal(t, "high", entry.Priority)
		assert.Equal(t, "medium", entry.Complexity)
		assert.Equal(t, []string{"tag1", "tag2"}, entry.Labels)
		assert.Equal(t, "2024-01-01", entry.Updated)
		assert.Equal(t, "backlog/FEAT-001-test.md", entry.Path)
	})

	t.Run("parse row with simple path", func(t *testing.T) {
		row := "| FEAT-001 | Test Feature | backlog | alice | high | medium | tag1 | 2024-01-01 | backlog/FEAT-001-test.md |"

		entry, err := parseTableRow(row)
		require.NoError(t, err)

		assert.Equal(t, "backlog/FEAT-001-test.md", entry.Path)
	})

	t.Run("parse row with empty labels", func(t *testing.T) {
		row := "| FEAT-001 | Test Feature | backlog | alice | high | medium |  | 2024-01-01 | backlog/FEAT-001-test.md |"

		entry, err := parseTableRow(row)
		require.NoError(t, err)

		assert.Empty(t, entry.Labels)
	})

	t.Run("parse row with insufficient columns", func(t *testing.T) {
		row := "| FEAT-001 | Test Feature |"

		entry, err := parseTableRow(row)
		require.NoError(t, err)

		// Should return empty entry
		assert.Equal(t, "", entry.ID)
	})

	t.Run("parse row without pipes", func(t *testing.T) {
		row := "FEAT-001 Test Feature backlog"

		entry, err := parseTableRow(row)
		require.NoError(t, err)

		// Should return empty entry
		assert.Equal(t, "", entry.ID)
	})

	t.Run("parse row with extra whitespace", func(t *testing.T) {
		row := "|  FEAT-001  |  Test Feature  |  backlog  |  alice  |  high  |  medium  |  tag1, tag2  |  2024-01-01  |  backlog/FEAT-001-test.md  |"

		entry, err := parseTableRow(row)
		require.NoError(t, err)

		assert.Equal(t, "FEAT-001", entry.ID)
		assert.Equal(t, "Test Feature", entry.Title)
		assert.Equal(t, []string{"tag1", "tag2"}, entry.Labels)
	})
}
