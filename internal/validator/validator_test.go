package validator

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/testutil"
	"github.com/virtualboard/vb-cli/internal/util"
)

func TestApplyFixesPreauthorizesBoardAndRollsBackWriteFailure(t *testing.T) {
	t.Run("authorization", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		mgr := feature.NewManager(opts)
		first := newFeature(mgr, "FTR-0700", "backlog", "First", nil)
		second := newFeature(mgr, "FTR-0701", "backlog", "Second", nil)
		second.FrontMatter.Owner = "another-owner"
		writeFeature(t, fix, first)
		writeFeature(t, fix, second)
		before, _ := os.ReadFile(first.Path)
		v, err := New(opts, mgr)
		if err != nil {
			t.Fatal(err)
		}
		features, err := v.CollectFeatures(first.FrontMatter.ID, second.FrontMatter.ID)
		if err != nil {
			t.Fatal(err)
		}
		processorCalls := 0
		if err := v.ApplyFixes(features, func(candidate *feature.Feature) error {
			processorCalls++
			candidate.FrontMatter.Title += " fixed"
			return nil
		}); !errors.Is(err, feature.ErrOwnershipConflict) {
			t.Fatalf("ownership conflict was not fail-closed: %v", err)
		}
		if processorCalls != 0 {
			t.Fatalf("processor ran before all authorization completed: %d", processorCalls)
		}
		after, _ := os.ReadFile(first.Path)
		if !bytes.Equal(before, after) {
			t.Fatal("authorization failure partially repaired another feature")
		}
	})

	t.Run("write rollback", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		mgr := feature.NewManager(opts)
		first := newFeature(mgr, "FTR-0710", "backlog", "First", nil)
		second := newFeature(mgr, "FTR-0711", "backlog", "Second", nil)
		writeFeature(t, fix, first)
		writeFeature(t, fix, second)
		beforeFirst, _ := os.ReadFile(first.Path)
		beforeSecond, _ := os.ReadFile(second.Path)
		v, err := New(opts, mgr)
		if err != nil {
			t.Fatal(err)
		}
		features, err := v.CollectFeatures(first.FrontMatter.ID, second.FrontMatter.ID)
		if err != nil {
			t.Fatal(err)
		}
		originalWriter := replaceValidatedFix
		calls := 0
		replaceValidatedFix = func(batch *feature.MutationBatch, plan plannedFix, expected, replacement []byte) error {
			calls++
			if calls == 2 {
				return errors.New("injected second fix failure")
			}
			return originalWriter(batch, plan, expected, replacement)
		}
		t.Cleanup(func() { replaceValidatedFix = originalWriter })
		if err := v.ApplyFixes(features, func(candidate *feature.Feature) error {
			candidate.FrontMatter.Title += " fixed"
			return nil
		}); err == nil {
			t.Fatal("injected fix failure was ignored")
		}
		afterFirst, _ := os.ReadFile(first.Path)
		afterSecond, _ := os.ReadFile(second.Path)
		if !bytes.Equal(beforeFirst, afterFirst) || !bytes.Equal(beforeSecond, afterSecond) {
			t.Fatal("fix write failure left a partial repair")
		}
	})
}

func TestApplyFixesHoldsFeatureGuardsThroughApplyAndRollback(t *testing.T) {
	t.Run("apply", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		mgr := feature.NewManager(opts)
		first := newFeature(mgr, "FTR-0720", "backlog", "First", nil)
		second := newFeature(mgr, "FTR-0721", "backlog", "Second", nil)
		writeFeature(t, fix, first)
		writeFeature(t, fix, second)
		v, err := New(opts, mgr)
		if err != nil {
			t.Fatal(err)
		}
		features, err := v.CollectFeatures(first.FrontMatter.ID, second.FrontMatter.ID)
		if err != nil {
			t.Fatal(err)
		}

		originalReplacer := replaceValidatedFix
		entered := make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		calls := 0
		replaceValidatedFix = func(batch *feature.MutationBatch, plan plannedFix, expected, replacement []byte) error {
			calls++
			err := originalReplacer(batch, plan, expected, replacement)
			if calls == 1 && err == nil {
				close(entered)
				<-release
			}
			return err
		}
		t.Cleanup(func() {
			releaseOnce.Do(func() { close(release) })
			replaceValidatedFix = originalReplacer
		})

		applyDone := make(chan error, 1)
		go func() {
			applyDone <- v.ApplyFixes(features, func(candidate *feature.Feature) error {
				candidate.FrontMatter.Title += " fixed"
				return nil
			})
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("validate --fix did not enter the guarded apply phase")
		}

		observed := make(chan string, 1)
		contenderDone := make(chan error, 1)
		go func() {
			_, mutateErr := mgr.MutateFeature(first.FrontMatter.ID, func(current *feature.Feature) error {
				observed <- current.FrontMatter.Title
				current.FrontMatter.Title = "contender"
				return nil
			})
			contenderDone <- mutateErr
		}()
		select {
		case err := <-contenderDone:
			t.Fatalf("concurrent mutation bypassed validate --fix apply guard: %v", err)
		case <-time.After(100 * time.Millisecond):
		}

		releaseOnce.Do(func() { close(release) })
		if err := <-applyDone; err != nil {
			t.Fatalf("validate --fix apply failed: %v", err)
		}
		if err := <-contenderDone; err != nil {
			t.Fatalf("serialized mutation failed: %v", err)
		}
		if title := <-observed; title != "First fixed" {
			t.Fatalf("serialized mutation observed title %q, want applied title", title)
		}
	})

	t.Run("rollback", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		mgr := feature.NewManager(opts)
		first := newFeature(mgr, "FTR-0730", "backlog", "First", nil)
		second := newFeature(mgr, "FTR-0731", "backlog", "Second", nil)
		writeFeature(t, fix, first)
		writeFeature(t, fix, second)
		v, err := New(opts, mgr)
		if err != nil {
			t.Fatal(err)
		}
		features, err := v.CollectFeatures(first.FrontMatter.ID, second.FrontMatter.ID)
		if err != nil {
			t.Fatal(err)
		}

		originalReplacer := replaceValidatedFix
		rollbackEntered := make(chan struct{})
		releaseRollback := make(chan struct{})
		var releaseOnce sync.Once
		calls := 0
		replaceValidatedFix = func(batch *feature.MutationBatch, plan plannedFix, expected, replacement []byte) error {
			calls++
			switch calls {
			case 2:
				return errors.New("injected second fix failure")
			case 3:
				close(rollbackEntered)
				<-releaseRollback
			}
			return originalReplacer(batch, plan, expected, replacement)
		}
		t.Cleanup(func() {
			releaseOnce.Do(func() { close(releaseRollback) })
			replaceValidatedFix = originalReplacer
		})

		applyDone := make(chan error, 1)
		go func() {
			applyDone <- v.ApplyFixes(features, func(candidate *feature.Feature) error {
				candidate.FrontMatter.Title += " fixed"
				return nil
			})
		}()
		select {
		case <-rollbackEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("validate --fix did not enter guarded rollback")
		}

		observed := make(chan string, 1)
		contenderDone := make(chan error, 1)
		go func() {
			_, mutateErr := mgr.MutateFeature(first.FrontMatter.ID, func(current *feature.Feature) error {
				observed <- current.FrontMatter.Title
				current.FrontMatter.Title = "contender"
				return nil
			})
			contenderDone <- mutateErr
		}()
		select {
		case err := <-contenderDone:
			t.Fatalf("concurrent mutation bypassed validate --fix rollback guard: %v", err)
		case <-time.After(100 * time.Millisecond):
		}

		releaseOnce.Do(func() { close(releaseRollback) })
		if err := <-applyDone; err == nil || !strings.Contains(err.Error(), "injected second fix failure") {
			t.Fatalf("validate --fix rollback error = %v", err)
		}
		if err := <-contenderDone; err != nil {
			t.Fatalf("serialized mutation failed: %v", err)
		}
		if title := <-observed; title != "First" {
			t.Fatalf("serialized mutation observed title %q, want rolled-back title", title)
		}
	})
}

func TestValidatorWorkflow(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)

	valid := newFeature(mgr, "FTR-0001", "backlog", "Valid Feature", []string{"alpha"})
	writeFeature(t, fix, valid)

	mismatch := newFeature(mgr, "FTR-0002", "in-progress", "Mismatch Feature", nil)
	mismatch.Path = filepath.Join(opts.RootDir, "features", "backlog", "FTR-0002-wrong.md")
	mismatch.FrontMatter.Status = "in-progress"
	writeFeature(t, fix, mismatch)

	invalid := newFeature(mgr, "FTR-0003", "review", "Invalid Dates", nil)
	invalid.FrontMatter.Created = "2023-99-99"
	invalid.FrontMatter.Updated = "2023-13-40"
	invalid.Path = filepath.Join(opts.RootDir, "features", "review", "FTR-0003-wrongname.md")
	writeFeature(t, fix, invalid)

	dup := newFeature(mgr, "FTR-0001", "backlog", "Duplicate", nil)
	dup.Path = filepath.Join(opts.RootDir, "features", "backlog", "FTR-0001-duplicate.md")
	writeFeature(t, fix, dup)
	unique := newFeature(mgr, "FTR-0004", "backlog", "Unique", nil)
	writeFeature(t, fix, unique)

	cycleA := newFeature(mgr, "FTR-0100", "backlog", "Cycle A", nil)
	cycleA.FrontMatter.Dependencies = []string{"FTR-0101"}
	writeFeature(t, fix, cycleA)
	cycleB := newFeature(mgr, "FTR-0101", "backlog", "Cycle B", nil)
	cycleB.FrontMatter.Dependencies = []string{"FTR-0100"}
	writeFeature(t, fix, cycleB)

	v, err := New(opts, mgr)
	if err != nil {
		t.Fatalf("validator init failed: %v", err)
	}
	summary, err := v.ValidateAll()
	if err != nil {
		t.Fatalf("validate all failed: %v", err)
	}
	if summary.Total == 0 || summary.Invalid == 0 {
		t.Fatalf("expected invalid features: %+v", summary)
	}
	if !summary.HasErrors() {
		t.Fatalf("expected summary to report errors")
	}
	if summary.Error() == nil {
		t.Fatalf("expected summary error output")
	}

	result, err := v.ValidateID("FTR-0004")
	if err != nil {
		t.Fatalf("validate id failed: %v", err)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("expected valid feature, got errors: %v", result.Errors)
	}

	if _, err := v.ValidateID("unknown"); !errors.Is(err, feature.ErrIdentityMismatch) {
		t.Fatalf("expected malformed identity error, got %v", err)
	}

	collection, err := v.CollectFeatures()
	if err != nil || len(collection) != summary.Total {
		t.Fatalf("collect all failed: %v %d", err, len(collection))
	}

	filtered, err := v.CollectFeatures("FTR-0100")
	if err != nil || len(filtered) != 1 {
		t.Fatalf("collect specific failed: %v %d", err, len(filtered))
	}

	applyCalled := false
	immutablePath := filtered["FTR-0100"].Path
	if err := v.ApplyFixes(filtered, func(feat *feature.Feature) error {
		applyCalled = true
		feat.FrontMatter.Owner = "fixed"
		return nil
	}); err != nil {
		t.Fatalf("apply fixes failed: %v", err)
	}
	if !applyCalled {
		t.Fatalf("expected processor to be invoked")
	}
	if filtered["FTR-0100"].FrontMatter.Updated != time.Now().Format("2006-01-02") {
		t.Fatalf("validate --fix did not update the content timestamp")
	}
	if filtered["FTR-0100"].Path != immutablePath {
		t.Fatalf("validate --fix renamed immutable feature path")
	}
}

func TestValidatorReportsBodyStructureAndStatusReadiness(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)
	validator, err := New(opts, mgr)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("duplicate canonical section", func(t *testing.T) {
		candidate := newFeature(mgr, "FTR-0800", "backlog", "Duplicate Body", nil)
		candidate.Body = strings.Replace(candidate.Body, "</untrusted-content>", "## Summary\nDuplicate summary.\n\n</untrusted-content>", 1)
		result := validator.validateSingle(candidate)
		if !containsValidationError(result.Errors, `canonical section "Summary" appears 2 times`) {
			t.Fatalf("validation errors = %#v", result.Errors)
		}
	})

	t.Run("boundary misordering", func(t *testing.T) {
		candidate := newFeature(mgr, "FTR-0801", "backlog", "Boundary Body", nil)
		candidate.Body = strings.Replace(candidate.Body, "<untrusted-content>", "BOUNDARY", 1)
		candidate.Body = strings.Replace(candidate.Body, "</untrusted-content>", "<untrusted-content>", 1)
		candidate.Body = strings.Replace(candidate.Body, "BOUNDARY", "</untrusted-content>", 1)
		result := validator.validateSingle(candidate)
		if !containsValidationError(result.Errors, "opening marker must precede") {
			t.Fatalf("validation errors = %#v", result.Errors)
		}
	})

	t.Run("review readiness", func(t *testing.T) {
		candidate := newFeature(mgr, "FTR-0802", "review", "Review Body", nil)
		candidate.Body = strings.Replace(candidate.Body, "- [x] Feature validation has test coverage.", "- [ ] Feature validation has test coverage.", 1)
		result := validator.validateSingle(candidate)
		if !containsValidationError(result.Errors, "must be checked before review") {
			t.Fatalf("validation errors = %#v", result.Errors)
		}
	})
}

func TestValidateIDTraversesFullDependencyClosure(t *testing.T) {
	t.Run("transitive missing dependency", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		mgr := feature.NewManager(opts)
		root := newFeature(mgr, "FTR-0810", "backlog", "Closure Root", nil)
		root.FrontMatter.Dependencies = []string{"FTR-0811"}
		middle := newFeature(mgr, "FTR-0811", "backlog", "Closure Middle", nil)
		middle.FrontMatter.Dependencies = []string{"FTR-0812"}
		leaf := newFeature(mgr, "FTR-0812", "backlog", "Closure Leaf", nil)
		leaf.FrontMatter.Dependencies = []string{"FTR-0899"}
		for _, feat := range []*feature.Feature{root, middle, leaf} {
			writeFeature(t, fix, feat)
		}
		validator, err := New(opts, mgr)
		if err != nil {
			t.Fatal(err)
		}
		result, err := validator.ValidateID(root.FrontMatter.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := "dependency chain FTR-0810 -> FTR-0811 -> FTR-0812 -> FTR-0899: dependency FTR-0899 not found"
		if !containsValidationError(result.Errors, want) {
			t.Fatalf("transitive missing dependency lacks full chain:\n%v", result.Errors)
		}
	})

	t.Run("transitive cycle and invalid dependency", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		mgr := feature.NewManager(opts)
		root := newFeature(mgr, "FTR-0820", "backlog", "Cycle Closure Root", nil)
		root.FrontMatter.Dependencies = []string{"FTR-0821"}
		first := newFeature(mgr, "FTR-0821", "backlog", "Cycle Closure First", nil)
		first.FrontMatter.Dependencies = []string{"FTR-0822"}
		second := newFeature(mgr, "FTR-0822", "backlog", "Cycle Closure Second", nil)
		second.FrontMatter.Dependencies = []string{"FTR-0821"}
		second.FrontMatter.Updated = "invalid-date"
		for _, feat := range []*feature.Feature{root, first, second} {
			writeFeature(t, fix, feat)
		}
		validator, err := New(opts, mgr)
		if err != nil {
			t.Fatal(err)
		}
		result, err := validator.ValidateID(root.FrontMatter.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !containsValidationError(result.Errors, "circular dependency detected in reachable chain: FTR-0820 -> FTR-0821 -> FTR-0822 -> FTR-0821") {
			t.Fatalf("transitive cycle lacks reachable chain: %v", result.Errors)
		}
		if !containsValidationError(result.Errors, "dependency chain FTR-0820 -> FTR-0821 -> FTR-0822: updated date must be YYYY-MM-DD") {
			t.Fatalf("transitive validation error lacks chain: %v", result.Errors)
		}
	})

	t.Run("transitive dependency readiness", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		mgr := feature.NewManager(opts)
		root := newFeature(mgr, "FTR-0825", "backlog", "Readiness Closure Root", nil)
		root.FrontMatter.Dependencies = []string{"FTR-0826"}
		middle := newFeature(mgr, "FTR-0826", "in-progress", "Readiness Closure Middle", nil)
		middle.FrontMatter.Dependencies = []string{"FTR-0827"}
		leaf := newFeature(mgr, "FTR-0827", "backlog", "Readiness Closure Leaf", nil)
		for _, feat := range []*feature.Feature{root, middle, leaf} {
			writeFeature(t, fix, feat)
		}
		validator, err := New(opts, mgr)
		if err != nil {
			t.Fatal(err)
		}
		result, err := validator.ValidateID(root.FrontMatter.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := "dependency chain FTR-0825 -> FTR-0826 -> FTR-0827: dependency FTR-0827 must be done before FTR-0826 can be in-progress"
		if !containsValidationError(result.Errors, want) {
			t.Fatalf("transitive readiness error lacks full chain: %v", result.Errors)
		}
	})
}

func TestValidatorEnforcesMaximumDependencyDepth(t *testing.T) {
	t.Run("ten dependency edges are allowed", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		mgr := feature.NewManager(opts)
		rootID := writeDependencyChain(t, fix, mgr, 860, maxDependencyDepth)
		validator, err := New(opts, mgr)
		if err != nil {
			t.Fatal(err)
		}
		result, err := validator.ValidateID(rootID)
		if err != nil {
			t.Fatal(err)
		}
		if containsValidationError(result.Errors, "maximum dependency depth") {
			t.Fatalf("depth-%d dependency chain was rejected: %v", maxDependencyDepth, result.Errors)
		}
	})

	t.Run("eleventh dependency edge is rejected everywhere", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		mgr := feature.NewManager(opts)
		rootID := writeDependencyChain(t, fix, mgr, 900, maxDependencyDepth+1)
		validator, err := New(opts, mgr)
		if err != nil {
			t.Fatal(err)
		}
		result, err := validator.ValidateID(rootID)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, maxDependencyDepth+2)
		for offset := 0; offset <= maxDependencyDepth+1; offset++ {
			ids = append(ids, fmt.Sprintf("FTR-%04d", 900+offset))
		}
		want := "dependency chain " + strings.Join(ids, " -> ") + " exceeds maximum dependency depth 10"
		if !containsValidationError(result.Errors, want) {
			t.Fatalf("over-depth ValidateID error = %v, want %q", result.Errors, want)
		}

		summary, err := validator.ValidateAll()
		if err != nil {
			t.Fatal(err)
		}
		if !containsValidationError(summary.Results[rootID].Errors, want) {
			t.Fatalf("over-depth ValidateAll error = %v, want %q", summary.Results[rootID].Errors, want)
		}
	})
}

func writeDependencyChain(t *testing.T, fix *testutil.Fixture, mgr *feature.Manager, firstID, edgeCount int) string {
	t.Helper()
	for offset := 0; offset <= edgeCount; offset++ {
		id := fmt.Sprintf("FTR-%04d", firstID+offset)
		candidate := newFeature(mgr, id, "backlog", fmt.Sprintf("Dependency Depth %d", offset), nil)
		if offset < edgeCount {
			candidate.FrontMatter.Dependencies = []string{fmt.Sprintf("FTR-%04d", firstID+offset+1)}
		}
		writeFeature(t, fix, candidate)
	}
	return fmt.Sprintf("FTR-%04d", firstID)
}

func TestValidatorEnforcesDateProvenance(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)
	validator, err := New(opts, mgr)
	if err != nil {
		t.Fatal(err)
	}
	future := "2999-01-01"
	tests := []struct {
		name          string
		created       string
		statusChanged string
		updated       string
		want          string
	}{
		{name: "created after status change", created: "2025-02-02", statusChanged: "2025-02-01", updated: "2025-02-03", want: "created <= status_changed"},
		{name: "status change after update", created: "2025-02-01", statusChanged: "2025-02-03", updated: "2025-02-02", want: "status_changed <= updated"},
		{name: "future created", created: future, statusChanged: future, updated: future, want: "created date cannot be in the future"},
		{name: "future status change", created: "2025-02-01", statusChanged: future, updated: future, want: "status_changed date cannot be in the future"},
		{name: "future update", created: "2025-02-01", statusChanged: "2025-02-01", updated: future, want: "updated date cannot be in the future"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := newFeature(mgr, fmt.Sprintf("FTR-%04d", 830+index), "backlog", test.name, nil)
			candidate.FrontMatter.Created = test.created
			candidate.FrontMatter.StatusChanged = test.statusChanged
			candidate.FrontMatter.Updated = test.updated
			result := validator.validateSingle(candidate)
			if !containsValidationError(result.Errors, test.want) {
				t.Fatalf("date errors = %v, want %q", result.Errors, test.want)
			}
		})
	}
}

func TestValidatorRequiresReviewerSeparation(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)
	validator, err := New(opts, mgr)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"review", "done"} {
		t.Run(status, func(t *testing.T) {
			candidate := newFeature(mgr, "FTR-0840", status, "Reviewer Separation", nil)
			candidate.FrontMatter.Owner = "implementer"
			candidate.FrontMatter.ImplementationOwner = "implementer"
			result := validator.validateSingle(candidate)
			if !containsValidationError(result.Errors, "requires reviewer owner to differ from implementation_owner") {
				t.Fatalf("same reviewer/implementer accepted for %s: %v", status, result.Errors)
			}
			candidate.FrontMatter.Owner = "reviewer"
			result = validator.validateSingle(candidate)
			if containsValidationError(result.Errors, "requires reviewer owner to differ") {
				t.Fatalf("distinct reviewer rejected for %s: %v", status, result.Errors)
			}
		})
	}
}

func newFeature(mgr *feature.Manager, id, status, title string, labels []string) *feature.Feature {
	statusDir := filepath.Join(mgr.FeaturesDir(), filepath.Base(feature.DirectoryForStatus(status)))
	return &feature.Feature{
		Path: filepath.Join(statusDir, fmt.Sprintf("%s-%s.md", id, util.Slugify(title))),
		FrontMatter: feature.FrontMatter{
			ID:     id,
			Title:  title,
			Status: status,
			Owner:  "owner",
			ImplementationOwner: func() string {
				if status == "backlog" {
					return "unassigned"
				}
				return "owner"
			}(),
			Priority:      "P2",
			Complexity:    "S",
			Created:       "2023-01-01",
			Updated:       "2023-01-01",
			StatusChanged: "2023-01-01",
			Labels:        labels,
			Dependencies:  []string{},
		},
		Body: validFeatureBody(),
	}
}

func validFeatureBody() string {
	sections := map[string]string{
		"Summary":                        "Concrete summary.",
		"Problem Statement":              "Concrete problem statement.",
		"Goals & Non-Goals":              "- Goal: validate the feature.",
		"User Stories":                   "- As a user, I can validate the feature.",
		"Requirements":                   "### Functional\n- Validate the feature.",
		"Acceptance Criteria (Testable)": "- [x] Feature validation has test coverage.",
		"UI/UX Notes":                    "- CLI-only behavior.",
		"Data & API":                     "- No data changes.",
		"Rollout & Migration":            "- Test-only rollout.",
		"Monitoring & Metrics":           "- Test result recorded.",
		"Security & Compliance":          "- Body text is untrusted.",
		"Implementation Notes":           "- Implemented and covered by focused tests.",
		"Open Questions":                 "- None remain.",
		"Links":                          "- FTR-0001 validator test reference.",
	}
	var body strings.Builder
	body.WriteString("<untrusted-content>\n\n")
	for _, name := range feature.CanonicalSectionOrder() {
		fmt.Fprintf(&body, "## %s\n%s\n\n", name, sections[name])
	}
	body.WriteString("</untrusted-content>\n")
	return body.String()
}

func writeFeature(t *testing.T, fix *testutil.Fixture, feat *feature.Feature) {
	data, err := feat.Encode()
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	workspace := filepath.Join(fix.Root, ".virtualboard")
	rel, err := filepath.Rel(workspace, feat.Path)
	if err != nil {
		t.Fatalf("rel failed: %v", err)
	}
	fix.WriteFile(t, rel, data)
}
