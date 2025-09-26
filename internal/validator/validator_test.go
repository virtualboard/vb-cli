package validator

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/testutil"
	"github.com/virtualboard/vb-cli/internal/util"
)

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

	if _, err := v.ValidateID("unknown"); !errors.Is(err, feature.ErrNotFound) {
		t.Fatalf("expected not found error, got %v", err)
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
}

func newFeature(mgr *feature.Manager, id, status, title string, labels []string) *feature.Feature {
	statusDir := filepath.Join(mgr.FeaturesDir(), filepath.Base(feature.DirectoryForStatus(status)))
	return &feature.Feature{
		Path: filepath.Join(statusDir, fmt.Sprintf("%s-%s.md", id, util.Slugify(title))),
		FrontMatter: feature.FrontMatter{
			ID:           id,
			Title:        title,
			Status:       status,
			Owner:        "owner",
			Priority:     "medium",
			Complexity:   "low",
			Created:      "2023-01-01",
			Updated:      "2023-01-01",
			Labels:       labels,
			Dependencies: []string{},
		},
		Body: "## Summary\n\nSummary\n\n## Details\n\nDetails\n",
	}
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
