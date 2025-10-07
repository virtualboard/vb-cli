package feature

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/virtualboard/vb-cli/internal/testutil"
)

// TestMoveFeatureUpdatesStatus verifies that moving a feature updates the status in frontmatter
func TestMoveFeatureUpdatesStatus(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	// Create a feature in backlog
	feat, err := mgr.CreateFeature("Test Feature", nil)
	if err != nil {
		t.Fatalf("create feature failed: %v", err)
	}

	initialID := feat.FrontMatter.ID
	initialStatus := feat.FrontMatter.Status
	if initialStatus != "backlog" {
		t.Fatalf("expected initial status 'backlog', got '%s'", initialStatus)
	}

	// Move feature to in-progress
	movedFeat, _, err := mgr.MoveFeature(initialID, "in-progress", "testowner")
	if err != nil {
		t.Fatalf("move feature failed: %v", err)
	}

	// Verify the returned feature has updated status
	if movedFeat.FrontMatter.Status != "in-progress" {
		t.Fatalf("expected returned feature status 'in-progress', got '%s'", movedFeat.FrontMatter.Status)
	}

	// Verify the file is in the correct directory
	expectedDir := filepath.Join(opts.RootDir, DirectoryForStatus("in-progress"))
	actualDir := filepath.Dir(movedFeat.Path)
	if actualDir != expectedDir {
		t.Fatalf("expected file in directory %s, got %s", expectedDir, actualDir)
	}

	// CRITICAL: Reload from disk and verify status was persisted
	reloaded, err := mgr.LoadByID(initialID)
	if err != nil {
		t.Fatalf("reload feature failed: %v", err)
	}

	if reloaded.FrontMatter.Status != "in-progress" {
		t.Fatalf("CRITICAL: status in file not updated! Expected 'in-progress', got '%s'", reloaded.FrontMatter.Status)
	}

	// Verify owner was updated
	if reloaded.FrontMatter.Owner != "testowner" {
		t.Fatalf("expected owner 'testowner', got '%s'", reloaded.FrontMatter.Owner)
	}

	// Verify the file content directly
	data, err := os.ReadFile(reloaded.Path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	fileContent := string(data)
	if !containsString(fileContent, "status: in-progress") {
		t.Fatalf("file content does not contain 'status: in-progress': %s", fileContent)
	}
}

// TestMoveFeatureMultipleTransitions verifies status updates across multiple moves
func TestMoveFeatureMultipleTransitions(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	// Create a feature
	feat, err := mgr.CreateFeature("Multi Move Feature", nil)
	if err != nil {
		t.Fatalf("create feature failed: %v", err)
	}

	id := feat.FrontMatter.ID

	// Track transitions: backlog → in-progress → review → done
	transitions := []struct {
		from string
		to   string
	}{
		{"backlog", "in-progress"},
		{"in-progress", "review"},
		{"review", "done"},
	}

	for _, tr := range transitions {
		// Move to next status
		_, _, err := mgr.MoveFeature(id, tr.to, "")
		if err != nil {
			t.Fatalf("move from %s to %s failed: %v", tr.from, tr.to, err)
		}

		// Reload and verify
		reloaded, err := mgr.LoadByID(id)
		if err != nil {
			t.Fatalf("reload after move to %s failed: %v", tr.to, err)
		}

		if reloaded.FrontMatter.Status != tr.to {
			t.Fatalf("after moving to %s, reloaded status is '%s'", tr.to, reloaded.FrontMatter.Status)
		}

		// Verify file is in correct directory
		expectedDir := filepath.Join(opts.RootDir, DirectoryForStatus(tr.to))
		actualDir := filepath.Dir(reloaded.Path)
		if actualDir != expectedDir {
			t.Fatalf("after moving to %s, file in %s instead of %s", tr.to, actualDir, expectedDir)
		}
	}
}

// TestMoveFeatureAtomicConsistency verifies that the file in the new location
// always has the correct status from the moment it exists (no inconsistency window)
func TestMoveFeatureAtomicConsistency(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	// Create a feature
	feat, err := mgr.CreateFeature("Atomic Test", nil)
	if err != nil {
		t.Fatalf("create feature failed: %v", err)
	}

	id := feat.FrontMatter.ID
	oldPath := feat.Path

	// Move to in-progress
	movedFeat, _, err := mgr.MoveFeature(id, "in-progress", "")
	if err != nil {
		t.Fatalf("move failed: %v", err)
	}

	newPath := movedFeat.Path

	// Verify old file is gone
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old file should not exist at %s", oldPath)
	}

	// Verify new file exists and has correct status
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new file should exist at %s: %v", newPath, err)
	}

	// Read directly from disk and verify status is correct
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("failed to read new file: %v", err)
	}

	fileContent := string(data)
	if !containsString(fileContent, "status: in-progress") {
		t.Fatalf("new file should contain 'status: in-progress' but got: %s", fileContent)
	}

	// Parse and verify
	parsed, err := Parse(newPath, data)
	if err != nil {
		t.Fatalf("failed to parse new file: %v", err)
	}

	if parsed.FrontMatter.Status != "in-progress" {
		t.Fatalf("parsed status should be 'in-progress', got '%s'", parsed.FrontMatter.Status)
	}
}

func containsString(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 ||
		(len(haystack) > 0 && len(needle) > 0 && findSubstring(haystack, needle)))
}

func findSubstring(haystack, needle string) bool {
	for i := 0; i <= len(haystack)-len(needle); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
