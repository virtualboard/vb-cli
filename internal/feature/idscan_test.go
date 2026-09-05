package feature

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/virtualboard/vb-cli/internal/testutil"
)

// git runs a git command in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Scrub inherited GIT_* state. When the suite runs from a git hook, GIT_DIR
	// and GIT_INDEX_FILE point at the outer repository and these commands would
	// operate on it instead of the fixture.
	cmd.Env = append(scrubGitEnv(os.Environ()),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

// initRepo turns the fixture root into a git repository with one commit.
func initRepo(t *testing.T, root string) {
	t.Helper()
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "board")
}

// writeFeature drops a minimally-valid feature file into a backlog directory.
func writeFeature(t *testing.T, backlogDir, id string) {
	t.Helper()
	if err := os.MkdirAll(backlogDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(backlogDir, id+"-some-feature.md")
	if err := os.WriteFile(path, []byte("---\nid: "+id+"\n---\n"), 0o600); err != nil {
		t.Fatalf("write feature: %v", err)
	}
}

// A board that is not a git repository at all must keep working — git failures
// degrade to the local-only scan rather than breaking `vb new`.
func TestNextIDOutsideGitRepo(t *testing.T) {
	fix := testutil.NewFixture(t)
	mgr := NewManager(fix.Options(t, false, false, false))

	writeFeature(t, filepath.Join(mgr.FeaturesDir(), "backlog"), "FTR-0007")

	got, err := mgr.NextID()
	if err != nil {
		t.Fatalf("NextID failed: %v", err)
	}
	if got != "FTR-0008" {
		t.Fatalf("expected FTR-0008 from the local scan, got %s", got)
	}
}

// The FTR-0139 incident: an id committed on a branch that the current branch
// does not contain must still be treated as taken.
func TestNextIDSeesIDsOnOtherBranches(t *testing.T) {
	fix := testutil.NewFixture(t)
	mgr := NewManager(fix.Options(t, false, false, false))
	backlog := filepath.Join(mgr.FeaturesDir(), "backlog")

	initRepo(t, fix.Root)

	// A feature is created and committed on a side branch...
	git(t, fix.Root, "checkout", "-q", "-b", "side")
	writeFeature(t, backlog, "FTR-0042")
	git(t, fix.Root, "add", "-A")
	git(t, fix.Root, "commit", "-q", "-m", "add FTR-0042")

	// ...then we return to a branch where that file does not exist.
	git(t, fix.Root, "checkout", "-q", "main")
	if _, err := os.Stat(filepath.Join(backlog, "FTR-0042-some-feature.md")); !os.IsNotExist(err) {
		t.Fatalf("expected FTR-0042 to be absent from the working tree")
	}

	got, err := mgr.NextID()
	if err != nil {
		t.Fatalf("NextID failed: %v", err)
	}
	if got != "FTR-0043" {
		t.Fatalf("expected FTR-0043 (0042 is taken on another branch), got %s", got)
	}
}

// Renumbering renames a file rather than adding one. A history scan filtered to
// additions misses those ids entirely, which is why --diff-filter=A is not used.
func TestNextIDSeesRenumberedIDsInHistory(t *testing.T) {
	fix := testutil.NewFixture(t)
	mgr := NewManager(fix.Options(t, false, false, false))
	backlog := filepath.Join(mgr.FeaturesDir(), "backlog")

	writeFeature(t, backlog, "FTR-0010")
	initRepo(t, fix.Root)

	// The board bot renumbers 0010 -> 0011 and the rename is committed.
	git(t, fix.Root, "mv",
		filepath.Join(backlog, "FTR-0010-some-feature.md"),
		filepath.Join(backlog, "FTR-0011-some-feature.md"))
	git(t, fix.Root, "commit", "-q", "-m", "renumber 0010 to 0011")

	// Then the renamed file is removed from the working tree.
	git(t, fix.Root, "rm", "-q", filepath.Join(backlog, "FTR-0011-some-feature.md"))
	git(t, fix.Root, "commit", "-q", "-m", "drop it")

	got, err := mgr.NextID()
	if err != nil {
		t.Fatalf("NextID failed: %v", err)
	}
	if got != "FTR-0012" {
		t.Fatalf("expected FTR-0012 (0011 was minted by a rename), got %s", got)
	}
}

// The FTR-0152 incident: two worktrees share one .git but have separate working
// trees, so an uncommitted feature in a sibling worktree is invisible both
// locally and in history. It must still be treated as taken.
func TestNextIDSeesSiblingWorktree(t *testing.T) {
	fix := testutil.NewFixture(t)
	mgr := NewManager(fix.Options(t, false, false, false))

	initRepo(t, fix.Root)

	sibling := filepath.Join(t.TempDir(), "wt")
	git(t, fix.Root, "worktree", "add", "-q", "-b", "feature-branch", sibling)

	// Created in the sibling worktree and deliberately NOT committed.
	writeFeature(t, filepath.Join(sibling, ".virtualboard", "features", "backlog"), "FTR-0099")

	got, err := mgr.NextID()
	if err != nil {
		t.Fatalf("NextID failed: %v", err)
	}
	if got != "FTR-0100" {
		t.Fatalf("expected FTR-0100 (0099 is uncommitted in a sibling worktree), got %s", got)
	}
}
