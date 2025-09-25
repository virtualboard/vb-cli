package template

import (
	"path/filepath"
	"testing"

	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestProcessorApply(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)

	processor, err := NewProcessor(mgr)
	if err != nil {
		t.Fatalf("processor init failed: %v", err)
	}

	feat := feature.Feature{
		Path: filepath.Join(opts.RootDir, feature.DirectoryForStatus("backlog"), "FTR-2000-needs-sections.md"),
		FrontMatter: feature.FrontMatter{
			ID:         "FTR-2000",
			Title:      "Needs Sections",
			Status:     "backlog",
			Owner:      "",
			Priority:   "",
			Complexity: "",
			Created:    "2023-01-01",
			Updated:    "2023-01-01",
		},
		Body: "Intro text\n\n",
	}
	feat.FrontMatter.Status = ""
	feat.FrontMatter.Labels = nil
	feat.FrontMatter.Dependencies = nil

	if err := processor.Apply(&feat); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if feat.FrontMatter.Labels == nil {
		t.Fatalf("expected labels initialised")
	}

	if err := processor.Apply(nil); err == nil {
		t.Fatalf("expected error for nil feature")
	}
}
