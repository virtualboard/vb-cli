package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/contract"
	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/testutil"
)

type indexJSONResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Checked bool   `json:"checked"`
		Written bool   `json:"written"`
		Drift   bool   `json:"drift"`
		Missing bool   `json:"missing"`
		DryRun  bool   `json:"dry_run"`
		Path    string `json:"path"`
	} `json:"data"`
}

func executeIndexCommand(t *testing.T, opts *config.Options, args ...string) (string, error) {
	t.Helper()
	config.SetCurrent(opts)
	cmd := newIndexCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(args)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	return output.String(), err
}

func decodeIndexResponse(t *testing.T, output string) indexJSONResponse {
	t.Helper()
	var response indexJSONResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatalf("decode index JSON %q: %v", output, err)
	}
	return response
}

func createIndexedFeature(t *testing.T, opts *config.Options) {
	t.Helper()
	mgr := feature.NewManager(opts)
	if _, err := mgr.CreateFeature("Deterministic Index", []string{"index", "stable"}); err != nil {
		t.Fatalf("create indexed feature: %v", err)
	}
}

func TestIndexCheckMissingFailsWithoutWriting(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	createIndexedFeature(t, opts)

	output, err := executeIndexCommand(t, opts, "--check")
	if err == nil || ExitCode(err) != ExitCodeValidation {
		t.Fatalf("missing index check error = %v", err)
	}
	if !strings.Contains(output, "Index is missing") {
		t.Fatalf("missing index output = %q", output)
	}
	indexPath := filepath.Join(opts.RootDir, "features", "INDEX.md")
	if _, statErr := os.Stat(indexPath); !os.IsNotExist(statErr) {
		t.Fatalf("--check wrote a missing index: %v", statErr)
	}
}

func TestSeededEmptyIndexMatchesCanonicalGenerator(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	if err := seedEmptyFeatureIndex(opts.RootDir); err != nil {
		t.Fatalf("seed empty index: %v", err)
	}
	if output, err := executeIndexCommand(t, opts, "--check"); err != nil || !strings.Contains(output, "up to date") {
		t.Fatalf("seeded index is not canonical: output=%q err=%v", output, err)
	}
}

func TestIndexCheckCleanAndDriftNeverWrite(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	createIndexedFeature(t, opts)
	if _, err := executeIndexCommand(t, opts); err != nil {
		t.Fatalf("generate canonical index: %v", err)
	}

	indexPath := filepath.Join(opts.RootDir, "features", "INDEX.md")
	canonical, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	infoBefore, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := executeIndexCommand(t, opts, "--check"); err != nil || !strings.Contains(output, "up to date") {
		t.Fatalf("clean index check: output=%q err=%v", output, err)
	}
	cleanAfter, err := os.ReadFile(indexPath)
	if err != nil || !bytes.Equal(cleanAfter, canonical) {
		t.Fatalf("clean check changed index: %v", err)
	}
	infoAfter, err := os.Stat(indexPath)
	if err != nil || !os.SameFile(infoBefore, infoAfter) {
		t.Fatalf("clean check replaced the index file: %v", err)
	}

	tampered := append(append([]byte{}, canonical...), []byte("\nmanual change\n")...)
	if err := os.WriteFile(indexPath, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := executeIndexCommand(t, opts, "--check")
	if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(output, "drift detected") {
		t.Fatalf("drift check: output=%q err=%v", output, err)
	}
	after, readErr := os.ReadFile(indexPath)
	if readErr != nil || !bytes.Equal(after, tampered) {
		t.Fatalf("drift check changed target: %v", readErr)
	}
}

func TestIndexCheckJSONReportsStateAccurately(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, true, false, false)
	createIndexedFeature(t, opts)

	output, err := executeIndexCommand(t, opts, "--check")
	if err == nil || ExitCode(err) != ExitCodeValidation {
		t.Fatalf("JSON missing check error = %v", err)
	}
	missing := decodeIndexResponse(t, output)
	if missing.Success || !missing.Data.Checked || missing.Data.Written || !missing.Data.Drift || !missing.Data.Missing || missing.Data.DryRun {
		t.Fatalf("unexpected missing payload: %+v", missing)
	}

	generatedOutput, err := executeIndexCommand(t, opts)
	if err != nil {
		t.Fatalf("generate JSON index: %v", err)
	}
	generated := decodeIndexResponse(t, generatedOutput)
	if !generated.Success || generated.Data.Checked || !generated.Data.Written || !generated.Data.Drift || !generated.Data.Missing || generated.Data.DryRun {
		t.Fatalf("unexpected generation payload: %+v", generated)
	}
	output, err = executeIndexCommand(t, opts, "--check")
	if err != nil {
		t.Fatalf("clean JSON check: %v", err)
	}
	clean := decodeIndexResponse(t, output)
	if !clean.Success || !clean.Data.Checked || clean.Data.Written || clean.Data.Drift || clean.Data.Missing || clean.Data.DryRun {
		t.Fatalf("unexpected clean payload: %+v", clean)
	}

	indexPath := filepath.Join(opts.RootDir, "features", "INDEX.md")
	if err := os.WriteFile(indexPath, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err = executeIndexCommand(t, opts, "--check")
	if err == nil || ExitCode(err) != ExitCodeValidation {
		t.Fatalf("JSON drift check error = %v", err)
	}
	drift := decodeIndexResponse(t, output)
	if drift.Success || !drift.Data.Checked || drift.Data.Written || !drift.Data.Drift || drift.Data.Missing {
		t.Fatalf("unexpected drift payload: %+v", drift)
	}
}

func TestIndexJSONDryRunAndCheckNeverReportWritten(t *testing.T) {
	fix := testutil.NewFixture(t)
	writeOpts := fix.Options(t, true, false, false)
	createIndexedFeature(t, writeOpts)
	if _, err := executeIndexCommand(t, writeOpts); err != nil {
		t.Fatal(err)
	}

	dryOpts := fix.Options(t, true, false, true)
	output, err := executeIndexCommand(t, dryOpts)
	if err != nil {
		t.Fatalf("JSON dry-run: %v", err)
	}
	dry := decodeIndexResponse(t, output)
	if dry.Data.Checked || dry.Data.Written || dry.Data.Drift || !dry.Data.DryRun {
		t.Fatalf("unexpected dry-run payload: %+v", dry)
	}

	output, err = executeIndexCommand(t, dryOpts, "--check")
	if err != nil {
		t.Fatalf("JSON dry-run check: %v", err)
	}
	checked := decodeIndexResponse(t, output)
	if !checked.Data.Checked || checked.Data.Written || checked.Data.Drift || !checked.Data.DryRun {
		t.Fatalf("unexpected dry-run check payload: %+v", checked)
	}
}

func TestIndexCheckRejectsStreamTargets(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	for _, args := range [][]string{
		{"--check", "--output", "-"},
		{"--check", "--format", "json"},
	} {
		if _, err := executeIndexCommand(t, opts, args...); err == nil || ExitCode(err) != ExitCodeValidation {
			t.Fatalf("args %v should require a file target: %v", args, err)
		}
	}
}

func TestIndexCheckSupportsExplicitNonMarkdownTargets(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	createIndexedFeature(t, opts)
	for _, format := range []string{"json", "html"} {
		t.Run(format, func(t *testing.T) {
			target := filepath.Join("features", "INDEX."+format)
			args := []string{"--format", format, "--output", target}
			if _, err := executeIndexCommand(t, opts, args...); err != nil {
				t.Fatalf("generate %s index: %v", format, err)
			}
			if _, err := executeIndexCommand(t, opts, append(args, "--check")...); err != nil {
				t.Fatalf("check %s index: %v", format, err)
			}
		})
	}
}

func TestIndexRejectsNoncanonicalFeaturePathContract(t *testing.T) {
	fix := testutil.NewFixture(t)
	contractJSON := strings.Replace(string(contract.CanonicalJSON()), `"features": "features"`, `"features": "work/items"`, 1)
	if err := os.WriteFile(fix.Path("virtualboard.json"), []byte(contractJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := fix.Options(t, false, false, false)

	if _, err := executeIndexCommand(t, opts); err == nil {
		t.Fatal("noncanonical feature path contract was accepted")
	}
}

func TestIndexRejectsOutputOutsideWorkspace(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	outside := filepath.Clean(filepath.Join(opts.RootDir, "../../outside-index.md"))
	if _, err := executeIndexCommand(t, opts, "--output", "../../outside-index.md"); err == nil || ExitCode(err) != ExitCodeValidation {
		t.Fatalf("traversing index output accepted: %v", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("index wrote outside workspace: %v", err)
	}

	outsideDir := t.TempDir()
	symlink := filepath.Join(opts.RootDir, "outside-link")
	if err := os.Symlink(outsideDir, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := executeIndexCommand(t, opts, "--output", "outside-link/INDEX.md"); err == nil || ExitCode(err) != ExitCodeValidation {
		t.Fatalf("symlinked external index output accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "INDEX.md")); !os.IsNotExist(err) {
		t.Fatalf("index followed symlink outside workspace: %v", err)
	}
}

func TestFormatStatusSummaryIsDeterministic(t *testing.T) {
	summary := map[string]int{"review": 3, "backlog": 1, "in-progress": 2}
	want := "1 backlog, 2 in-progress, 3 review"
	for i := 0; i < 100; i++ {
		if got := formatStatusSummary(summary); got != want {
			t.Fatalf("formatStatusSummary() = %q, want %q", got, want)
		}
	}
}
