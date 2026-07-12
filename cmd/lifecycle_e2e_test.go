package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/frameworkschema"
	"github.com/virtualboard/vb-cli/internal/testutil"
	"github.com/virtualboard/vb-cli/internal/validator"
)

func TestCLIProvenanceClaimAndValidate(t *testing.T) {
	fix := testutil.NewFixture(t)
	installProvenanceFixture(t, fix)
	opts, output := setupOptions(t, fix, false, false, false)
	opts.Actor = "agent-one"
	t.Setenv("VIRTUALBOARD_ACTOR", "")
	t.Setenv("AGENT_ID", "")

	newCmd := newNewCommand()
	newCmd.SetOut(output)
	newCmd.SetErr(output)
	newCmd.SetArgs([]string{"one two three four five six seven"})
	if err := newCmd.Execute(); err != nil {
		t.Fatalf("vb new: %v", err)
	}
	mgr := feature.NewManager(opts)
	features, err := mgr.List()
	if err != nil || len(features) != 1 {
		t.Fatalf("created features: %d %v", len(features), err)
	}
	created := features[0]
	if created.FrontMatter.ImplementationOwner != "unassigned" || created.FrontMatter.StatusChanged != time.Now().Format("2006-01-02") {
		t.Fatalf("new provenance: %+v", created.FrontMatter)
	}
	if filepath.Base(created.Path) != "FTR-0001-one-two-three-four-five-six.md" {
		t.Fatalf("unbounded feature slug: %s", created.Path)
	}

	lockCmd := newLockCommand()
	lockCmd.SetOut(output)
	lockCmd.SetErr(output)
	lockCmd.SetArgs([]string{created.FrontMatter.ID, "--owner", "agent-one"})
	if err := lockCmd.Execute(); err != nil {
		t.Fatalf("vb lock with established --owner syntax: %v", err)
	}
	moveCmd := newMoveCommand()
	moveCmd.SetOut(output)
	moveCmd.SetErr(output)
	moveCmd.SetArgs([]string{created.FrontMatter.ID, "in-progress", "--owner", "agent-one"})
	if err := moveCmd.Execute(); err != nil {
		t.Fatalf("vb move: %v", err)
	}

	moved, err := mgr.LoadByID(created.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if moved.FrontMatter.Owner != "agent-one" || moved.FrontMatter.ImplementationOwner != "agent-one" {
		t.Fatalf("claim provenance: %+v", moved.FrontMatter)
	}
	validateCmd := newValidateCommand()
	validateCmd.SetOut(output)
	validateCmd.SetErr(output)
	validateCmd.SetArgs([]string{created.FrontMatter.ID})
	if err := validateCmd.Execute(); err != nil {
		t.Fatalf("new -> lock -> move -> validate failed: %v\n%s", err, output.String())
	}
}

func TestMutationsRequireExplicitActor(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, output := setupOptions(t, fix, false, false, false)
	opts.Actor = ""
	t.Setenv("VIRTUALBOARD_ACTOR", "")
	t.Setenv("AGENT_ID", "")
	t.Setenv("USER", "shared-os-user")

	newCmd := newNewCommand()
	newCmd.SilenceErrors = true
	newCmd.SilenceUsage = true
	newCmd.SetOut(output)
	newCmd.SetErr(output)
	newCmd.SetArgs([]string{"Missing Actor"})
	if err := newCmd.Execute(); ExitCode(err) != ExitCodeValidation {
		t.Fatalf("new without actor exit = %d (%v)", ExitCode(err), err)
	}

	lockCmd := newLockCommand()
	lockCmd.SilenceErrors = true
	lockCmd.SilenceUsage = true
	lockCmd.SetOut(output)
	lockCmd.SetErr(output)
	lockCmd.SetArgs([]string{"FTR-0001", "--owner", "victim"})
	if err := lockCmd.Execute(); ExitCode(err) != ExitCodeValidation {
		t.Fatalf("--owner impersonated actor; exit = %d (%v)", ExitCode(err), err)
	}
}

func TestLockRejectsTraversalIDWithoutWritingOutsideWorkspace(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, output := setupOptions(t, fix, false, false, false)
	opts.Actor = "safe-actor"
	lockCmd := newLockCommand()
	lockCmd.SilenceErrors = true
	lockCmd.SilenceUsage = true
	lockCmd.SetOut(output)
	lockCmd.SetErr(output)
	lockCmd.SetArgs([]string{"../../escaped", "--owner", "safe-actor"})
	if err := lockCmd.Execute(); ExitCode(err) != ExitCodeValidation {
		t.Fatalf("traversal lock exit = %d (%v)", ExitCode(err), err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(opts.RootDir), "escaped.lock")); !os.IsNotExist(err) {
		t.Fatalf("lock traversal wrote outside workspace: %v", err)
	}
}

func TestLockAcquireRejectsNonOwnerWithoutExplicitForce(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, output := setupOptions(t, fix, false, false, false)
	opts.Actor = "alice"
	mgr := feature.NewManager(opts)
	feat, err := mgr.CreateFeature("Owned Lock", nil)
	if err != nil {
		t.Fatal(err)
	}
	feat.FrontMatter.Owner = "alice"
	if err := mgr.Save(feat); err != nil {
		t.Fatal(err)
	}

	opts.Actor = "mallory"
	lockCmd := newLockCommand()
	lockCmd.SilenceErrors = true
	lockCmd.SilenceUsage = true
	lockCmd.SetOut(output)
	lockCmd.SetErr(output)
	lockCmd.SetArgs([]string{feat.FrontMatter.ID})
	if err := lockCmd.Execute(); ExitCode(err) != ExitCodeLockConflict {
		t.Fatalf("non-owner lock exit = %d (%v)", ExitCode(err), err)
	}
	if _, err := os.Stat(filepath.Join(mgr.LocksDir(), feat.FrontMatter.ID+".lock")); !os.IsNotExist(err) {
		t.Fatalf("non-owner lock acquisition wrote a lock: %v", err)
	}
}

func TestUpdateRejectsLifecycleManagedFieldsWithoutWriting(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, output := setupOptions(t, fix, false, false, false)
	mgr := feature.NewManager(opts)
	created, err := mgr.CreateFeature("Immutable Fields", nil)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(created.Path)
	if err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{"id", "status", "owner", "created", "updated", "implementation_owner", "status_changed"} {
		output.Reset()
		updateCmd := newUpdateCommand()
		updateCmd.SilenceErrors = true
		updateCmd.SilenceUsage = true
		updateCmd.SetOut(output)
		updateCmd.SetErr(output)
		updateCmd.SetArgs([]string{created.FrontMatter.ID, "--field", field + "=forbidden"})
		if err := updateCmd.Execute(); ExitCode(err) != ExitCodeValidation {
			t.Fatalf("field %s exit = %d (%v)", field, ExitCode(err), err)
		}
		after, err := os.ReadFile(created.Path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(after, original) {
			t.Fatalf("field %s mutated feature despite rejection", field)
		}
	}
}

func TestCreateAndUpdateRejectInvalidCandidatesBeforeWriting(t *testing.T) {
	fix := testutil.NewFixture(t)
	installProvenanceFixture(t, fix)
	opts, output := setupOptions(t, fix, false, false, false)
	opts.Actor = "validator-agent"

	invalidNew := newNewCommand()
	invalidNew.SilenceErrors = true
	invalidNew.SilenceUsage = true
	invalidNew.SetOut(output)
	invalidNew.SetErr(output)
	invalidNew.SetArgs([]string{"x"})
	if err := invalidNew.Execute(); ExitCode(err) != ExitCodeValidation {
		t.Fatalf("invalid new candidate exit = %d (%v)", ExitCode(err), err)
	}
	mgr := feature.NewManager(opts)
	if features, err := mgr.List(); err != nil || len(features) != 0 {
		t.Fatalf("invalid new candidate was persisted: %d, %v", len(features), err)
	}

	valid, err := mgr.CreateFeatureValidated("Valid Candidate", nil, func(candidate *feature.Feature) error {
		v, err := validator.New(opts, mgr)
		if err != nil {
			return err
		}
		return v.ValidateCandidate(candidate)
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(valid.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, update := range []string{"priority=INVALID", "dependencies=FTR-9999"} {
		output.Reset()
		command := newUpdateCommand()
		command.SilenceErrors = true
		command.SilenceUsage = true
		command.SetOut(output)
		command.SetErr(output)
		command.SetArgs([]string{valid.FrontMatter.ID, "--field", update})
		if err := command.Execute(); ExitCode(err) != ExitCodeValidation {
			t.Fatalf("invalid update %s exit = %d (%v)", update, ExitCode(err), err)
		}
		after, err := os.ReadFile(valid.Path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("invalid update %s changed feature bytes", update)
		}
	}
}

func TestTemplateApplyUpdatesContentDateOnly(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, output := setupOptions(t, fix, false, false, false)
	mgr := feature.NewManager(opts)
	created, err := mgr.CreateFeature("Template Timestamp", nil)
	if err != nil {
		t.Fatal(err)
	}
	created.FrontMatter.Updated = "2020-01-01"
	created.FrontMatter.Created = "2019-12-30"
	created.FrontMatter.StatusChanged = "2019-12-31"
	if err := mgr.Save(created); err != nil {
		t.Fatal(err)
	}

	templateCmd := newTemplateApplyCommand()
	templateCmd.SetOut(output)
	templateCmd.SetErr(output)
	templateCmd.SetArgs([]string{created.FrontMatter.ID})
	if err := templateCmd.Execute(); err != nil {
		t.Fatalf("template apply: %v", err)
	}
	reloaded, err := mgr.LoadByID(created.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.FrontMatter.Updated != time.Now().Format("2006-01-02") {
		t.Fatalf("template apply left stale updated date: %s", reloaded.FrontMatter.Updated)
	}
	if reloaded.FrontMatter.StatusChanged != "2019-12-31" {
		t.Fatalf("template apply changed lifecycle date: %s", reloaded.FrontMatter.StatusChanged)
	}
}

func installProvenanceFixture(t *testing.T, fix *testutil.Fixture) {
	t.Helper()
	template := `---
id: TEMPLATE
title: Template Feature
status: backlog
owner: unassigned
implementation_owner: unassigned
priority: P2
complexity: M
created: 2023-01-01
updated: 2023-01-01
status_changed: 2023-01-01
labels: []
dependencies: []
risk_notes: ""
---
` + canonicalFeatureBodyForTest()
	if err := os.WriteFile(fix.Path("templates", "feature.md"), []byte(template), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fix.Path("schemas", "frontmatter.schema.json"), frameworkschema.CanonicalFeature(), 0o600); err != nil {
		t.Fatal(err)
	}
}
