package validator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestValidateInternalLinks(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)
	feat := newFeature(mgr, "FTR-0200", "backlog", "Linked Feature", nil)
	feat.Body += "\n[existing](../../specs/existing.md) [missing](../../specs/missing.md) [web](https://example.com)\n"
	writeFeature(t, fix, feat)
	if err := os.WriteFile(filepath.Join(opts.RootDir, "specs", "existing.md"), []byte("# Existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := New(opts, mgr)
	if err != nil {
		t.Fatal(err)
	}
	result, err := v.ValidateID(feat.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !containsValidationError(result.Errors, "internal link target not found: ../../specs/missing.md") {
		t.Fatalf("missing-link error not found: %v", result.Errors)
	}
	if containsValidationError(result.Errors, "existing.md") || containsValidationError(result.Errors, "example.com") {
		t.Fatalf("valid/external link rejected: %v", result.Errors)
	}
}

func TestValidateInternalLinksRejectsWorkspaceEscapes(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)
	feat := newFeature(mgr, "FTR-0202", "backlog", "Contained Links", nil)

	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.md")
	if err := os.WriteFile(outsidePath, []byte("# Outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(opts.RootDir, "specs", "outside-link.md")
	if err := os.Symlink(outsidePath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	insidePath := filepath.Join(opts.RootDir, "specs", "inside.md")
	if err := os.WriteFile(insidePath, []byte("# Inside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(insidePath, filepath.Join(opts.RootDir, "specs", "inside-link.md")); err != nil {
		t.Fatal(err)
	}
	feat.Body += "\n" + strings.Join([]string{
		"[lexical escape](../../../outside.md)",
		"[symlink escape](../../specs/outside-link.md)",
		"[file scheme](file:///etc/passwd)",
		"[drive escape](C:/outside.md)",
		`[UNC escape](\\server\share\outside.md)`,
		`[rooted backslash escape](\outside.md)`,
		"[outer [nested label]](../../../outside-nested-label.md)",
		"[balanced parentheses](folder(name)/../../../../outside-balanced-parentheses.md)",
		`[escaped separators](..\/..\/..\/outside-escaped-separators.md)`,
		"[entity separators](..&#47;..&#47;..&#47;outside-entity-separators.md)",
		"[contained symlink](../../specs/inside-link.md)",
		"[anchor](#acceptance-criteria-testable)",
		"[mail](mailto:owner@example.invalid)",
		"[external](https://example.com/path)",
	}, " ") + "\n[reference escape]: ../../../outside-reference.md\n[outer [nested reference]]: ../../../outside-nested-reference.md\n[reference anchor]: #section\n[reference external]: https://example.com/reference\n`[inline code](../../../outside-inline-code.md)`\n```markdown\n[fenced code](../../../outside-fenced-code.md)\n[fenced reference]: ../../../outside-fenced-reference.md\n```\n\\`[escaped backticks do not hide a link](../../../outside-escaped-backtick.md)\\`\n    ```\n[indented pseudo-fence does not hide a link](../../../outside-indented-fence.md)\n```bad`info\n[invalid pseudo-fence does not hide a link](../../../outside-invalid-fence.md)\n"

	validator, err := New(opts, mgr)
	if err != nil {
		t.Fatal(err)
	}
	errors := validator.validateInternalLinks(feat)
	for _, want := range []string{
		"internal link escapes workspace: ../../../outside.md",
		"internal link resolves outside workspace: ../../specs/outside-link.md",
		"local file link scheme is not allowed: file:///etc/passwd",
		"internal link escapes workspace: C:/outside.md",
		`internal link escapes workspace: \server\share\outside.md`,
		`internal link escapes workspace: \outside.md`,
		"internal link escapes workspace: ../../../outside-nested-label.md",
		"internal link escapes workspace: folder(name)/../../../../outside-balanced-parentheses.md",
		"internal link escapes workspace: ../../../outside-escaped-separators.md",
		"internal link escapes workspace: ../../../outside-entity-separators.md",
		"internal link escapes workspace: ../../../outside-reference.md",
		"internal link escapes workspace: ../../../outside-nested-reference.md",
		"internal link escapes workspace: ../../../outside-escaped-backtick.md",
		"internal link escapes workspace: ../../../outside-indented-fence.md",
		"internal link escapes workspace: ../../../outside-invalid-fence.md",
	} {
		if !containsValidationError(errors, want) {
			t.Fatalf("link errors = %v, want %q", errors, want)
		}
	}
	for _, allowed := range []string{"inside-link.md", "#acceptance-criteria-testable", "mailto:", "https://", "outside-inline-code.md", "outside-fenced-code.md", "outside-fenced-reference.md"} {
		if containsValidationError(errors, allowed) {
			t.Fatalf("allowed link %q was rejected: %v", allowed, errors)
		}
	}
}

func TestValidateInternalLinksIsBounded(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)
	feat := newFeature(mgr, "FTR-0203", "backlog", "Bounded Links", nil)
	feat.Body = strings.Repeat("[anchor](#section)\n", maxMarkdownLinks+1)
	validator, err := New(opts, mgr)
	if err != nil {
		t.Fatal(err)
	}
	errors := validator.validateInternalLinks(feat)
	if !containsValidationError(errors, "more than 1024 Markdown links") {
		t.Fatalf("unbounded link inventory was accepted: %v", errors)
	}
}

func TestValidateUpdatedDateForGitDiff(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)
	feat := newFeature(mgr, "FTR-0201", "backlog", "Git Updated Feature", nil)
	writeFeature(t, fix, feat)

	runGit(t, fix.Root, "init")
	runGit(t, fix.Root, "add", ".virtualboard")
	runGit(t, fix.Root, "-c", "user.name=VirtualBoard Test", "-c", "user.email=vb@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "baseline")

	v, err := New(opts, mgr)
	if err != nil {
		t.Fatal(err)
	}
	clean, err := v.ValidateID(feat.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if containsValidationError(clean.Errors, "updated date must be today") {
		t.Fatalf("clean feature required a fresh date: %v", clean.Errors)
	}

	loaded, err := mgr.LoadByID(feat.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Body += "\nUncommitted edit.\n"
	if err := mgr.Save(loaded); err != nil {
		t.Fatal(err)
	}
	dirty, err := v.ValidateID(feat.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !containsValidationError(dirty.Errors, "updated date must be today") {
		t.Fatalf("dirty feature did not require today's date: %v", dirty.Errors)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func containsValidationError(errors []string, substring string) bool {
	for _, validationError := range errors {
		if strings.Contains(validationError, substring) {
			return true
		}
	}
	return false
}
