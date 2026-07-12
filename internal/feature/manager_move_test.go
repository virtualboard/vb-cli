package feature

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestMoveFeatureEnforcesReviewAndDoneBodyReadinessBeforeWriting(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	created, err := mgr.CreateFeature("Readiness Gate", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.MoveFeature(created.FrontMatter.ID, "in-progress", opts.Actor); err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.MutateFeature(created.FrontMatter.ID, func(candidate *Feature) error {
		return candidate.SetSection("Acceptance Criteria (Testable)", "- [ ] …")
	}); err != nil {
		t.Fatal(err)
	}
	beforeReview, err := mgr.LoadByID(created.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeReviewBytes, err := os.ReadFile(beforeReview.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.MoveFeature(created.FrontMatter.ID, "review", opts.Actor); !errors.Is(err, ErrStatusReadiness) {
		t.Fatalf("review readiness error = %v", err)
	}
	afterRejectedReview, err := os.ReadFile(beforeReview.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeReviewBytes, afterRejectedReview) {
		t.Fatal("rejected review transition changed feature bytes")
	}
	if _, err := os.Stat(filepath.Join(opts.RootDir, DirectoryForStatus("review"), filepath.Base(beforeReview.Path))); !os.IsNotExist(err) {
		t.Fatalf("rejected review transition created a destination file: %v", err)
	}

	if _, err := mgr.MutateFeature(created.FrontMatter.ID, func(candidate *Feature) error {
		return candidate.SetSection("Acceptance Criteria (Testable)", "- [x] Review readiness is covered by a focused test.")
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.MoveFeature(created.FrontMatter.ID, "review", opts.Actor); !errors.Is(err, ErrOwnershipConflict) {
		t.Fatalf("same-owner review handoff error = %v, want ErrOwnershipConflict", err)
	}
	if _, _, err := mgr.MoveFeature(created.FrontMatter.ID, "review", strings.ToUpper(opts.Actor)); !errors.Is(err, ErrOwnershipConflict) {
		t.Fatalf("case-variant self-review error = %v, want ErrOwnershipConflict", err)
	}
	stillInProgress, err := mgr.LoadByID(created.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillInProgress.FrontMatter.Status != "in-progress" || stillInProgress.FrontMatter.Owner != opts.Actor {
		t.Fatalf("rejected same-owner review changed lifecycle: %+v", stillInProgress.FrontMatter)
	}
	if _, _, err := mgr.MoveFeature(created.FrontMatter.ID, "review", "reviewer"); err != nil {
		t.Fatalf("ready review transition failed: %v", err)
	}
	opts.Actor = "reviewer"

	if _, err := mgr.MutateFeature(created.FrontMatter.ID, func(candidate *Feature) error {
		if err := candidate.SetSection("Implementation Notes", "- Libraries, patterns, risks, tech debt considerations."); err != nil {
			return err
		}
		return candidate.SetSection("Links", "- Related FTRs, tickets, PRs.")
	}); err != nil {
		t.Fatal(err)
	}
	beforeDone, err := mgr.LoadByID(created.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeDoneBytes, err := os.ReadFile(beforeDone.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.MoveFeature(created.FrontMatter.ID, "done", "reviewer"); !errors.Is(err, ErrStatusReadiness) {
		t.Fatalf("done evidence error = %v", err)
	}
	afterRejectedDone, err := os.ReadFile(beforeDone.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeDoneBytes, afterRejectedDone) {
		t.Fatal("rejected done transition changed feature bytes")
	}

	if _, err := mgr.MutateFeature(created.FrontMatter.ID, func(candidate *Feature) error {
		if err := candidate.SetSection("Implementation Notes", "- Implemented the readiness gate and ran focused manager tests."); err != nil {
			return err
		}
		return candidate.SetSection("Links", "- FTR-0001 implementation reference.")
	}); err != nil {
		t.Fatal(err)
	}
	completed, _, err := mgr.MoveFeature(created.FrontMatter.ID, "done", "reviewer")
	if err != nil {
		t.Fatalf("ready done transition failed: %v", err)
	}
	if completed.FrontMatter.Status != "done" {
		t.Fatalf("status = %q, want done", completed.FrontMatter.Status)
	}
}

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
	if feat.FrontMatter.ImplementationOwner != "unassigned" || feat.FrontMatter.StatusChanged != time.Now().Format("2006-01-02") {
		t.Fatalf("new feature provenance is incomplete: %+v", feat.FrontMatter)
	}

	// Move feature to in-progress
	opts.Actor = "testowner"
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
	if reloaded.FrontMatter.ImplementationOwner != "testowner" {
		t.Fatalf("expected implementation owner 'testowner', got %q", reloaded.FrontMatter.ImplementationOwner)
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
		from  string
		to    string
		owner string
	}{
		{"backlog", "in-progress", ""},
		{"in-progress", "review", "reviewer"},
		{"review", "done", "reviewer"},
	}

	for _, tr := range transitions {
		// Move to next status
		_, _, err := mgr.MoveFeature(id, tr.to, tr.owner)
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
		if tr.to == "review" {
			opts.Actor = "reviewer"
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

func TestImplementationOwnerSurvivesReviewHandoff(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.Actor = "implementer"
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("Review Handoff", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", "implementer"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "review", "reviewer"); err != nil {
		t.Fatal(err)
	}

	opts.Actor = "reviewer"
	if _, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", "different-implementer"); !errors.Is(err, ErrOwnershipConflict) {
		t.Fatalf("reviewer reassigned implementation ownership through ordinary move: %v", err)
	}
	resumed, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", "")
	if err != nil {
		t.Fatalf("resume using preserved implementation owner: %v", err)
	}
	if resumed.FrontMatter.Owner != "implementer" || resumed.FrontMatter.ImplementationOwner != "implementer" {
		t.Fatalf("reviewer was inferred as implementer: %+v", resumed.FrontMatter)
	}
}

func TestImplementationOwnerSurvivesBlockedReassignmentAndResume(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.Actor = "alice"
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("Blocked Provenance", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", "alice"); err != nil {
		t.Fatal(err)
	}
	blocked, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "blocked", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if blocked.FrontMatter.Owner != "bob" || blocked.FrontMatter.ImplementationOwner != "alice" {
		t.Fatalf("blocked handoff lost provenance: %+v", blocked.FrontMatter)
	}

	opts.Actor = "bob"
	resumed, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", "")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.FrontMatter.Owner != "bob" || resumed.FrontMatter.ImplementationOwner != "alice" {
		t.Fatalf("blocked resume reassigned implementation provenance: %+v", resumed.FrontMatter)
	}
}

func TestGenericUpdateDoesNotChangeStatusChanged(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("Stable Status Date", nil)
	if err != nil {
		t.Fatal(err)
	}
	feat.FrontMatter.StatusChanged = "2020-01-02"
	if err := mgr.Save(feat); err != nil {
		t.Fatal(err)
	}
	feat.FrontMatter.Title = "Content Update Only"
	if err := mgr.UpdateFeature(feat); err != nil {
		t.Fatal(err)
	}
	reloaded, err := mgr.LoadByID(feat.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.FrontMatter.StatusChanged != "2020-01-02" {
		t.Fatalf("generic update changed status_changed: %s", reloaded.FrontMatter.StatusChanged)
	}
}

func TestMoveDestinationFailureLeavesOriginalContent(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("Rename Rollback", nil)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(feat.Path)
	if err != nil {
		t.Fatal(err)
	}
	originalInstaller := installMoveDestination
	installMoveDestination = func(string, string, []byte, os.FileMode) error { return errors.New("injected destination failure") }
	t.Cleanup(func() { installMoveDestination = originalInstaller })

	if _, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", "owner"); err == nil {
		t.Fatal("expected injected move failure")
	}
	after, err := os.ReadFile(feat.Path)
	if err != nil {
		t.Fatalf("original feature missing after rollback: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("failed move mutated original feature\n--- original ---\n%s\n--- after ---\n%s", original, after)
	}
	newPath := filepath.Join(opts.RootDir, DirectoryForStatus("in-progress"), filepath.Base(feat.Path))
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatalf("failed move left destination file: %v", err)
	}
}

func TestMoveSourceRemovalFailureLeavesRecoverableJournalWithoutStatusMismatch(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.Actor = "owner"
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("Recoverable Move", nil)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := feat.Path
	newPath := filepath.Join(opts.RootDir, DirectoryForStatus("in-progress"), filepath.Base(oldPath))

	originalRemover := removeMoveSource
	removeMoveSource = func(string) error { return errors.New("injected source removal failure") }
	if _, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", "owner"); !errors.Is(err, ErrPendingMove) {
		removeMoveSource = originalRemover
		t.Fatalf("source removal failure did not leave a recoverable transaction: %v", err)
	}
	removeMoveSource = originalRemover
	t.Cleanup(func() { removeMoveSource = originalRemover })

	if _, err := os.Lstat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("captured source remained at its live lifecycle path: %v", err)
	}
	journalData, err := os.ReadFile(mgr.transactionPath(feat.FrontMatter.ID))
	if err != nil {
		t.Fatal(err)
	}
	var transaction featureMutationTransaction
	if err := json.Unmarshal(journalData, &transaction); err != nil {
		t.Fatal(err)
	}
	heldData, err := os.ReadFile(transaction.heldPath(opts.RootDir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(heldData), "status: in-progress") {
		t.Fatal("captured source was rewritten with destination status")
	}
	newData, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(newData), "status: in-progress") {
		t.Fatal("destination lifecycle path did not contain destination status")
	}
	if _, err := os.Stat(mgr.transactionPath(feat.FrontMatter.ID)); err != nil {
		t.Fatalf("move journal missing: %v", err)
	}

	recovered, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", "owner")
	if err != nil {
		t.Fatalf("direct move retry did not recover interrupted transaction: %v", err)
	}
	if recovered.Path != newPath || recovered.FrontMatter.Status != "in-progress" {
		t.Fatalf("unexpected recovered feature: %+v", recovered)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("recovery left source file: %v", err)
	}
	if _, err := os.Stat(mgr.transactionPath(feat.FrontMatter.ID)); !os.IsNotExist(err) {
		t.Fatalf("recovery left journal: %v", err)
	}
}

func TestMoveRecoveryWaitsForLiveFeatureOperationGuard(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.Actor = "owner"
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("Guarded Move Recovery", nil)
	if err != nil {
		t.Fatal(err)
	}

	originalRemover := removeMoveSource
	removeMoveSource = func(string) error { return errors.New("injected source removal failure") }
	if _, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", "owner"); !errors.Is(err, ErrPendingMove) {
		removeMoveSource = originalRemover
		t.Fatalf("source removal failure did not leave a recovery journal: %v", err)
	}
	removeMoveSource = originalRemover
	t.Cleanup(func() { removeMoveSource = originalRemover })

	guardEntered := make(chan struct{})
	releaseGuard := make(chan struct{})
	guardDone := make(chan error, 1)
	go func() {
		guardDone <- mgr.lockMgr.WithOperationGuard(featureMutationLockID(feat.FrontMatter.ID), func() error {
			close(guardEntered)
			<-releaseGuard
			return nil
		})
	}()
	<-guardEntered

	recoveryDone := make(chan error, 1)
	go func() { recoveryDone <- mgr.recoverPendingMoves() }()
	select {
	case err := <-recoveryDone:
		close(releaseGuard)
		t.Fatalf("move recovery bypassed a live feature operation guard: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseGuard)
	if err := <-guardDone; err != nil {
		t.Fatalf("live operation guard: %v", err)
	}
	select {
	case err := <-recoveryDone:
		if err != nil {
			t.Fatalf("recovery after guard release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("move recovery did not proceed after the live guard released")
	}
	if _, err := os.Stat(mgr.transactionPath(feat.FrontMatter.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery left journal: %v", err)
	}
}

func TestLegacyMoveJournalMigratesBeforeRemovingLiveSource(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("Legacy move journal", nil)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := feat.Path
	original, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	feat.FrontMatter.Status = "in-progress"
	feat.FrontMatter.Owner = opts.Actor
	feat.FrontMatter.ImplementationOwner = opts.Actor
	planned, err := feat.Encode()
	if err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(opts.RootDir, DirectoryForStatus("in-progress"), filepath.Base(oldPath))
	if err := os.WriteFile(newPath, planned, 0o644); err != nil {
		t.Fatal(err)
	}
	oldRelative, err := filepath.Rel(opts.RootDir, oldPath)
	if err != nil {
		t.Fatal(err)
	}
	newRelative, err := filepath.Rel(opts.RootDir, newPath)
	if err != nil {
		t.Fatal(err)
	}
	legacy := moveTransaction{
		Version: moveTransactionVersion, ID: feat.FrontMatter.ID,
		OldPath: oldRelative, NewPath: newRelative,
		OriginalSHA256: digestBytes(original), PlannedSHA256: digestBytes(planned),
	}
	payload, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mgr.legacyMoveTransactionPath(feat.FrontMatter.ID), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mgr.recoverPendingMoves(); err != nil {
		t.Fatalf("recover legacy move: %v", err)
	}
	if _, err := os.Lstat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy source remains after migrated recovery: %v", err)
	}
	if data, err := os.ReadFile(newPath); err != nil || string(data) != string(planned) {
		t.Fatalf("legacy destination changed during recovery: %v", err)
	}
	for _, journal := range []string{mgr.legacyMoveTransactionPath(feat.FrontMatter.ID), mgr.transactionPath(feat.FrontMatter.ID)} {
		if _, err := os.Lstat(journal); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("recovery left journal %s: %v", journal, err)
		}
	}
}

func TestCreateFeatureCapsSlugWords(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("one two three four five six seven eight", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Base(feat.Path), "FTR-0001-one-two-three-four-five-six.md"; got != want {
		t.Fatalf("filename = %s, want %s", got, want)
	}
	if strings.Count(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(feat.Path), feat.FrontMatter.ID+"-"), ".md"), "-") != 5 {
		t.Fatalf("slug was not capped to six words: %s", feat.Path)
	}
}

func TestLegacyLongSlugRemainsLoadableWhileNewSlugsStayBounded(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("Legacy Filename Compatibility", nil)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(filepath.Dir(feat.Path), "FTR-0001-one-two-three-four-five-six-seven-eight.md")
	if err := os.Rename(feat.Path, legacyPath); err != nil {
		t.Fatal(err)
	}
	loaded, err := mgr.LoadByID(feat.FrontMatter.ID)
	if err != nil {
		t.Fatalf("v0.9 long-slug feature became unloadable: %v", err)
	}
	if loaded.Path != legacyPath {
		t.Fatalf("loaded path = %s, want %s", loaded.Path, legacyPath)
	}
}

func TestLoadByIDFailsOnDuplicateFiles(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("Duplicate Guard", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(feat.Path)
	if err != nil {
		t.Fatal(err)
	}
	duplicatePath := filepath.Join(opts.RootDir, DirectoryForStatus("review"), filepath.Base(feat.Path))
	if err := os.WriteFile(duplicatePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.LoadByID(feat.FrontMatter.ID); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate ID did not fail closed: %v", err)
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
