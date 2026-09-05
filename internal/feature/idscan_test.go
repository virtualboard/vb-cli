package feature

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/virtualboard/vb-cli/internal/testutil"
)

// git runs a git command in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	// Auto maintenance is the one thing git does asynchronously: `git commit`
	// may leave a detached gc writing into .git after the command returns, and a
	// file appearing while the temp directory is being removed fails cleanup with
	// ENOTEMPTY. Turn it off, and ignore the machine's own git configuration so a
	// developer's or a CI runner's settings cannot turn it back on.
	cmd := exec.Command("git", append([]string{
		"-c", "gc.auto=0",
		"-c", "maintenance.auto=false",
	}, args...)...)
	cmd.Dir = dir
	// Scrub inherited GIT_* state. When the suite runs from a git hook, GIT_DIR
	// and GIT_INDEX_FILE point at the outer repository and these commands would
	// operate on it instead of the fixture.
	cmd.Env = append(scrubGitEnv(os.Environ()),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
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

	// Tear the repository down before Go removes the temp directory. Go's own
	// cleanup gets one attempt and reports ENOTEMPTY if anything appears while it
	// walks the tree; retrying here absorbs a straggling git process instead of
	// failing the test that happened to run last.
	t.Cleanup(func() {
		for attempt := 0; attempt < 5; attempt++ {
			if err := os.RemoveAll(root); err == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
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

// stubGit puts a fake `git` first on PATH. The degradation paths below cannot
// be provoked with a real repository — `git rev-parse --show-prefix` does not
// fail once `--show-toplevel` has succeeded — so the failure is injected here.
func stubGit(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "git")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatalf("write stub git: %v", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod stub git: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Every git call in the worktree scan is optional: a failure means "no extra
// ids known", never a failed `vb new`.
func TestWorktreeMaxIDDegradesOnGitFailure(t *testing.T) {
	cases := []struct {
		name   string
		script string
	}{
		{
			name:   "toplevel fails",
			script: "#!/bin/sh\nexit 1\n",
		},
		{
			name:   "toplevel reports nothing",
			script: "#!/bin/sh\nexit 0\n",
		},
		{
			name: "prefix fails",
			script: "#!/bin/sh\n" +
				"case \"$*\" in \"rev-parse --show-toplevel\") echo /tmp; exit 0;; esac\n" +
				"exit 1\n",
		},
		{
			name: "worktree list fails",
			script: "#!/bin/sh\n" +
				"case \"$*\" in\n" +
				"  \"rev-parse --show-toplevel\") echo /tmp; exit 0;;\n" +
				"  \"rev-parse --show-prefix\") exit 0;;\n" +
				"esac\n" +
				"exit 1\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubGit(t, tc.script)
			if got := worktreeMaxID(t.TempDir()); got != 0 {
				t.Fatalf("expected 0 when git fails, got %d", got)
			}
		})
	}
}

// Output too large to scan is treated as no output rather than a hard failure,
// so a pathological repository still degrades to the local-only scan.
func TestRunGitRejectsUnscannableOutput(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	// One line larger than runGit's 4MB scanner buffer.
	if err := os.WriteFile(big, append([]byte(strings.Repeat("a", 5<<20)), '\n'), 0o600); err != nil {
		t.Fatalf("write oversized output: %v", err)
	}
	stubGit(t, "#!/bin/sh\ncat "+big+"\n")

	if lines, ok := runGit(dir, "log"); ok || lines != nil {
		t.Fatalf("expected the oversized line to be rejected, got ok=%v lines=%d", ok, len(lines))
	}
}

// sameDir resolves symlinks, and an unresolvable path is simply not the local
// tree — a pruned worktree must not be mistaken for it.
func TestSameDirWithUnresolvablePaths(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "gone")

	if !sameDir(dir, dir) {
		t.Fatal("identical paths must match")
	}
	if sameDir(missing, dir) {
		t.Fatal("an unresolvable first path must not match")
	}
	if sameDir(dir, missing) {
		t.Fatal("an unresolvable second path must not match")
	}
}

// A sibling worktree checked out from a branch without a board has nothing to
// contribute and must be skipped rather than block creation.
func TestNextIDSkipsWorktreeWithoutBoard(t *testing.T) {
	fix := testutil.NewFixture(t)
	mgr := NewManager(fix.Options(t, false, false, false))

	writeFeature(t, filepath.Join(mgr.FeaturesDir(), "backlog"), "FTR-0004")
	initRepo(t, fix.Root)

	sibling := filepath.Join(t.TempDir(), "wt")
	git(t, fix.Root, "worktree", "add", "-q", "-b", "boardless", sibling)
	if err := os.RemoveAll(filepath.Join(sibling, ".virtualboard")); err != nil {
		t.Fatalf("remove sibling board: %v", err)
	}

	got, err := mgr.NextID()
	if err != nil {
		t.Fatalf("NextID failed: %v", err)
	}
	if got != "FTR-0005" {
		t.Fatalf("expected FTR-0005 from the local scan alone, got %s", got)
	}
}

// A features directory that cannot be reached is a genuine failure, not an
// empty board: handing out FTR-0001 there would collide with every existing id.
func TestNextIDPropagatesUnreachableFeaturesDir(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	// Replace the workspace with a regular file so features/ resolves through a
	// non-directory (ENOTDIR) instead of merely being absent.
	if err := os.RemoveAll(opts.RootDir); err != nil {
		t.Fatalf("remove workspace: %v", err)
	}
	if err := os.WriteFile(opts.RootDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}

	if _, err := mgr.NextID(); err == nil {
		t.Fatal("expected NextID to fail when the features path is unreachable")
	}
}

// An unreadable status directory must fail the scan. Silently skipping it would
// under-report the highest id, which is exactly how an id gets minted twice.
func TestNextIDPropagatesUnreadableStatusDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}

	fix := testutil.NewFixture(t)
	mgr := NewManager(fix.Options(t, false, false, false))

	backlog := filepath.Join(mgr.FeaturesDir(), "backlog")
	if err := os.Chmod(backlog, 0o000); err != nil {
		t.Fatalf("chmod backlog: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(backlog, 0o750) })

	if _, err := mgr.NextID(); err == nil {
		t.Fatal("expected NextID to fail on an unreadable status directory")
	}
}

// A board with no features directory yet starts numbering at one.
func TestNextIDOnEmptyBoard(t *testing.T) {
	fix := testutil.NewFixture(t)
	mgr := NewManager(fix.Options(t, false, false, false))

	if err := os.RemoveAll(mgr.FeaturesDir()); err != nil {
		t.Fatalf("remove features dir: %v", err)
	}

	got, err := mgr.NextID()
	if err != nil {
		t.Fatalf("NextID failed: %v", err)
	}
	if got != "FTR-0001" {
		t.Fatalf("expected FTR-0001 on an empty board, got %s", got)
	}
}

// Files that are not feature specs live under features/ too (README, .gitkeep)
// and must not influence the next id.
func TestNextIDIgnoresNonFeatureFiles(t *testing.T) {
	fix := testutil.NewFixture(t)
	mgr := NewManager(fix.Options(t, false, false, false))

	backlog := filepath.Join(mgr.FeaturesDir(), "backlog")
	writeFeature(t, backlog, "FTR-0003")
	if err := os.WriteFile(filepath.Join(backlog, "README.md"), []byte("notes"), 0o600); err != nil {
		t.Fatalf("write stray file: %v", err)
	}

	got, err := mgr.NextID()
	if err != nil {
		t.Fatalf("NextID failed: %v", err)
	}
	if got != "FTR-0004" {
		t.Fatalf("expected FTR-0004, got %s", got)
	}
}
