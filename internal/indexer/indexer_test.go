package indexer

import (
	"bytes"
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
			ID:            "FTR-1000",
			Title:         "Indexed",
			Status:        "backlog",
			Owner:         "",
			Priority:      "P2",
			Complexity:    "S",
			Created:       "2023-01-01",
			Updated:       "2023-01-01",
			StatusChanged: "2023-01-02",
			Labels:        []string{"alpha"},
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
	if data.Features[0].StatusChanged != "2023-01-02" {
		t.Fatalf("status_changed not indexed: %#v", data.Features[0])
	}
	if data.Generated != GenerationMarker {
		t.Fatalf("generation marker = %q, want %q", data.Generated, GenerationMarker)
	}

	md, err := gen.Markdown(data)
	if err != nil || !strings.Contains(md, "Features Index") {
		t.Fatalf("markdown generation failed: %v\n%s", err, md)
	}
	if !strings.Contains(md, "Status Changed") || !strings.Contains(md, "2023-01-02") {
		t.Fatalf("markdown omitted status_changed: %s", md)
	}
	wantRelativeLink := "[backlog/FTR-1000-indexed.md](backlog/FTR-1000-indexed.md)"
	if !strings.Contains(md, wantRelativeLink) || strings.Contains(md, "../features/") {
		t.Fatalf("markdown link is not relative to the canonical index: %s", md)
	}

	jsonOutput, err := gen.JSON(data)
	if err != nil {
		t.Fatalf("json generation failed: %v", err)
	}
	var decoded Data
	if err := json.Unmarshal([]byte(jsonOutput), &decoded); err != nil {
		t.Fatalf("json parse failed: %v", err)
	}
	if len(decoded.Features) != 1 || decoded.Features[0].StatusChanged != "2023-01-02" {
		t.Fatalf("JSON omitted status_changed: %#v", decoded.Features)
	}

	html, err := gen.HTML(data)
	if err != nil || !strings.Contains(html, "<table>") || !strings.Contains(html, "Status Changed") || !strings.Contains(html, "2023-01-02") {
		t.Fatalf("html generation failed: %v", err)
	}
	if !strings.Contains(html, `href="backlog/FTR-1000-indexed.md"`) || strings.Contains(html, "../features/") {
		t.Fatalf("HTML link is not relative to the canonical index: %s", html)
	}

	dataAgain, err := gen.Build()
	if err != nil {
		t.Fatalf("second build failed: %v", err)
	}
	mdAgain, _ := gen.Markdown(dataAgain)
	jsonAgain, _ := gen.JSON(dataAgain)
	htmlAgain, _ := gen.HTML(dataAgain)
	if !bytes.Equal([]byte(md), []byte(mdAgain)) || jsonOutput != jsonAgain || html != htmlAgain {
		t.Fatal("unchanged feature state did not render byte-for-byte deterministic output")
	}
	if strings.Contains(md, "Auto-generated on") || strings.Contains(html, "generated 20") {
		t.Fatal("index output still contains wall-clock generation text")
	}
}

func TestMarkdownEscapesUntrustedTableContent(t *testing.T) {
	gen := &Generator{}
	data := &Data{
		Features: []Entry{{
			ID: "FTR-0001", Title: "row | injected\n| fake |", Status: "backlog",
			Owner: "<script>alert(1)</script>", Priority: "P2", Complexity: "M",
			Labels: []string{"safe", "x|y"}, Updated: "2026-01-01", StatusChanged: "2026-01-01",
			Path: "backlog/FTR-0001-safe.md",
		}},
		Summary: map[string]int{"backlog": 1},
	}
	markdown, err := gen.Markdown(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(markdown, "<script>") || strings.Contains(markdown, "\n| fake |") {
		t.Fatalf("untrusted index content escaped its table cell:\n%s", markdown)
	}
	for _, escaped := range []string{"row \\| injected", "x\\|y", "&lt;script&gt;"} {
		if !strings.Contains(markdown, escaped) {
			t.Fatalf("missing escaped content %q:\n%s", escaped, markdown)
		}
	}
}
