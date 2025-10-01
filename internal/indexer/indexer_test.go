package indexer

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestGeneratorBuildAndRender(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)

	feat := feature.Feature{
		Path: filepath.Join(opts.RootDir, feature.DirectoryForStatus("backlog"), "FTR-1000-indexed.md"),
		FrontMatter: feature.FrontMatter{
			ID:         "FTR-1000",
			Title:      "Indexed",
			Status:     "backlog",
			Owner:      "",
			Priority:   "medium",
			Complexity: "S",
			Created:    "2023-01-01",
			Updated:    "2023-01-01",
			Labels:     []string{"alpha"},
		},
		Body: "## Summary\n\nSummary\n\n## Details\n\nDetails\n",
	}
	encoded, err := feat.Encode()
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	workspace := filepath.Join(fix.Root, ".virtualboard")
	rel, err := filepath.Rel(workspace, feat.Path)
	if err != nil {
		t.Fatalf("rel failed: %v", err)
	}
	fix.WriteFile(t, rel, encoded)

	gen := NewGenerator(mgr)
	data, err := gen.Build()
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if len(data.Features) != 1 {
		t.Fatalf("expected one feature, got %d", len(data.Features))
	}

	md, err := gen.Markdown(data)
	if err != nil || !strings.Contains(md, "Features Index") {
		t.Fatalf("markdown generation failed: %v\n%s", err, md)
	}

	jsonOutput, err := gen.JSON(data)
	if err != nil {
		t.Fatalf("json generation failed: %v", err)
	}
	var decoded Data
	if err := json.Unmarshal([]byte(jsonOutput), &decoded); err != nil {
		t.Fatalf("json parse failed: %v", err)
	}

	html, err := gen.HTML(data)
	if err != nil || !strings.Contains(html, "<table>") {
		t.Fatalf("html generation failed: %v", err)
	}
}
