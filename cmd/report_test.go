package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestReportCommandDryRunAndPublication(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, _ := setupOptions(t, fix, true, false, true)
	templates := t.TempDir()
	if err := os.WriteFile(filepath.Join(templates, "report.html"), []byte(`<h1>{{TITLE}}</h1>`), 0o600); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(data, []byte(`{"TITLE":"<unsafe>"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "nested", "report.html")
	execute := func(extra ...string) (string, error) {
		t.Helper()
		cmd := newReportCommand()
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&bytes.Buffer{})
		args := []string{"render", "--template-dir", templates, "--template", "report.html", "--data", data, "--output", output}
		cmd.SetArgs(append(args, extra...))
		err := cmd.Execute()
		return out.String(), err
	}
	raw, err := execute()
	if err != nil {
		t.Fatal(err)
	}
	var preview struct {
		Success bool `json:"success"`
		Data    struct {
			DryRun bool `json:"dry_run"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &preview); err != nil || !preview.Success || !preview.Data.DryRun {
		t.Fatalf("bad preview: %s, %v", raw, err)
	}
	if _, err := os.Stat(filepath.Dir(output)); !os.IsNotExist(err) {
		t.Fatal("dry-run created output directory")
	}
	opts.DryRun = false
	if _, err := execute(); err != nil {
		t.Fatal(err)
	}
	rendered, err := os.ReadFile(output)
	if err != nil || string(rendered) != "<h1>&lt;unsafe&gt;</h1>" {
		t.Fatalf("unexpected artifact %s, %v", rendered, err)
	}
	if _, err := execute(); ExitCode(err) != ExitCodeFilesystem {
		t.Fatalf("existing report silently overwritten: %v", err)
	}
	if err := os.WriteFile(data, []byte(`{"TITLE":"updated"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := execute("--force"); err != nil {
		t.Fatal(err)
	}
	rendered, err = os.ReadFile(output)
	if err != nil || string(rendered) != "<h1>updated</h1>" {
		t.Fatal("explicit report replacement failed")
	}
	if err := os.WriteFile(data, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := execute("--force"); ExitCode(err) != ExitCodeValidation {
		t.Fatalf("missing values accepted: %v", err)
	}
	preserved, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(preserved, rendered) {
		t.Fatal("failed rendering changed prior report")
	}
	if err := os.WriteFile(data, []byte(`{"TITLE":"protected input"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{data, filepath.Join(templates, "report.html")} {
		output = input
		if _, err := execute("--force"); ExitCode(err) != ExitCodeValidation {
			t.Fatalf("input overwrite was allowed: %v", err)
		}
	}

}
