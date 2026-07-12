package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/virtualboard/vb-cli/internal/audit"
	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestMigrateLifecycleMetadataCommand(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, output := setupOptions(t, fix, false, false, false)
	opts.Actor = "reviewer"
	mgr := feature.NewManager(opts)
	legacy := &feature.Feature{
		Path: filepath.Join(mgr.FeaturesDir(), "review", "FTR-0400-legacy-review.md"),
		FrontMatter: feature.FrontMatter{
			ID: "FTR-0400", Title: "Legacy Review", Status: "review", Owner: "reviewer",
			Priority: "P2", Complexity: "M", Created: "2024-01-01", Updated: "2024-01-02",
			Labels: []string{}, Dependencies: []string{}, RiskNotes: "",
		},
		Body: "## Summary\n\nPreserve this body.\n",
	}
	data, err := legacy.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	migrateCmd := newMigrateLifecycleMetadataCommand()
	migrateCmd.SetOut(output)
	migrateCmd.SetErr(output)
	migrateCmd.SetArgs([]string{
		"--implementation-owner", "FTR-0400=implementer",
		"--status-changed", "FTR-0400=2024-01-03",
	})
	if err := migrateCmd.Execute(); err != nil {
		t.Fatalf("migrate lifecycle-metadata: %v\n%s", err, output.String())
	}
	migrated, err := mgr.LoadByID("FTR-0400")
	if err != nil {
		t.Fatal(err)
	}
	if migrated.FrontMatter.ImplementationOwner != "implementer" || migrated.FrontMatter.StatusChanged != "2024-01-03" {
		t.Fatalf("migration result: %+v", migrated.FrontMatter)
	}
	if migrated.Body != legacy.Body {
		t.Fatalf("migration changed body: %q", migrated.Body)
	}
	entries, parseErrors, err := audit.Read(filepath.Join(opts.RootDir, "audit.jsonl"))
	if err != nil || len(parseErrors) != 0 {
		t.Fatalf("read migration audit: %v %v", err, parseErrors)
	}
	foundAudit := false
	for _, entry := range entries {
		if entry.Action == "migrate-lifecycle-metadata" && entry.Actor == "reviewer" && entry.FeatureID == "FTR-0400" {
			foundAudit = true
		}
	}
	if !foundAudit {
		t.Fatalf("canonical migration audit event missing: %+v", entries)
	}
}

func TestMigrateLifecycleMetadataRequiresActorAndPreflights(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, output := setupOptions(t, fix, false, false, false)
	opts.Actor = ""
	t.Setenv("VIRTUALBOARD_ACTOR", "")
	t.Setenv("AGENT_ID", "")

	migrateCmd := newMigrateLifecycleMetadataCommand()
	migrateCmd.SilenceErrors = true
	migrateCmd.SilenceUsage = true
	migrateCmd.SetOut(output)
	migrateCmd.SetErr(output)
	if err := migrateCmd.Execute(); ExitCode(err) != ExitCodeValidation {
		t.Fatalf("migration without actor exit = %d (%v)", ExitCode(err), err)
	}

	if _, err := parseMigrationMappings([]string{"FTR-0001=a", "FTR-0001=b"}, "--implementation-owner"); err == nil {
		t.Fatal("conflicting mapping accepted")
	}
}
