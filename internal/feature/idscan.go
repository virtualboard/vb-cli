package feature

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// gitScanTimeout bounds each git invocation so a slow or wedged repository
// degrades to the local-only scan instead of hanging `vb new`.
const gitScanTimeout = 10 * time.Second

// maxIDIn returns the highest FTR id appearing in the supplied names, or 0.
func maxIDIn(names []string) int {
	maxID := 0
	for _, name := range names {
		matches := idPattern.FindStringSubmatch(filepath.Base(name))
		if len(matches) != 2 {
			continue
		}
		if n, err := strconv.Atoi(matches[1]); err == nil && n > maxID {
			maxID = n
		}
	}
	return maxID
}

// runGit executes a git command rooted at dir and returns its stdout lines.
// Any failure (git absent, not a repository, timeout) yields nil with ok=false
// so callers can fall back rather than fail.
func runGit(dir string, args ...string) ([]string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gitScanTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...) // #nosec G204 -- args are literals chosen below, never user input
	cmd.Dir = dir
	cmd.Env = scrubGitEnv(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}

	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	if scanner.Err() != nil {
		return nil, false
	}
	return lines, true
}

// historyMaxID returns the highest FTR id that has ever appeared under the
// features directory on any ref in the repository.
//
// `--diff-filter=A` is deliberately NOT used. Renumbering rewrites a feature's
// filename, and a rename is not an addition, so filtering to additions silently
// misses every id the board bot has ever renumbered into existence.
func historyMaxID(featuresDir string) int {
	lines, ok := runGit(featuresDir, "log", "--all", "--pretty=format:", "--name-only", "--", ".")
	if !ok {
		return 0
	}
	return maxIDIn(lines)
}

// worktreeMaxID returns the highest FTR id present in the working tree of any
// OTHER worktree attached to this repository.
//
// Git worktrees share one object store but have separate working trees, so a
// feature file created in a sibling worktree is invisible to both the local
// scan and to history until it is committed. That is the exact window in which
// FTR-0152 was minted twice, 69 seconds apart, by two worktrees.
func worktreeMaxID(featuresDir string) int {
	// Ask git for both halves rather than deriving them with filepath.Rel:
	// git reports the resolved toplevel, while featuresDir may still contain a
	// symlink (macOS temp dirs are /var -> /private/var), and Rel across the two
	// yields a bogus "../.." path.
	toplevel, ok := runGit(featuresDir, "rev-parse", "--show-toplevel")
	if !ok || len(toplevel) == 0 {
		return 0
	}
	prefix, ok := runGit(featuresDir, "rev-parse", "--show-prefix")
	if !ok {
		return 0
	}
	rel := ""
	if len(prefix) > 0 {
		rel = strings.TrimSuffix(prefix[0], "/")
	}

	lines, ok := runGit(featuresDir, "worktree", "list", "--porcelain")
	if !ok {
		return 0
	}

	maxID := 0
	for _, line := range lines {
		path, found := strings.CutPrefix(line, "worktree ")
		if !found {
			continue
		}
		if sameDir(path, toplevel[0]) {
			continue // the local tree is already scanned directly
		}
		names, err := listFeatureFiles(filepath.Join(path, rel))
		if err != nil {
			continue // a pruned or unreadable worktree must not block creation
		}
		if n := maxIDIn(names); n > maxID {
			maxID = n
		}
	}
	return maxID
}

// sameDir reports whether two paths refer to the same directory, tolerating
// symlinks such as macOS's /tmp -> /private/tmp.
func sameDir(a, b string) bool {
	if a == b {
		return true
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return ra == rb
}

// scrubGitEnv removes inherited GIT_* variables from an environment.
//
// `git commit` exports GIT_DIR and GIT_INDEX_FILE to its hooks, and they are
// usually relative (".git"). Any git command run from inside a hook would then
// resolve against the hook's repository rather than cmd.Dir, silently reading
// the wrong board. The repository is always determined by cmd.Dir here, so the
// inherited pointers are never wanted.
func scrubGitEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
