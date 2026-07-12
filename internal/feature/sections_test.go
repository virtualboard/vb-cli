package feature

import (
	"strings"
	"testing"
)

func canonicalBodyForTest(newline, acceptance, implementationNotes, links string) string {
	content := map[string]string{
		"Summary":                        "A concrete summary of the feature.",
		"Problem Statement":              "Users need deterministic feature lifecycle behavior.",
		"Goals & Non-Goals":              "- Goal: enforce the contract.",
		"User Stories":                   "- As a maintainer, I can validate feature evidence.",
		"Requirements":                   "### Functional" + newline + "- Enforce the documented behavior.",
		"Acceptance Criteria (Testable)": acceptance,
		"UI/UX Notes":                    "- The CLI prints actionable validation errors.",
		"Data & API":                     "- No persistent API changes.",
		"Rollout & Migration":            "- Roll out with the next CLI release.",
		"Monitoring & Metrics":           "- CI records the validation result.",
		"Security & Compliance":          "- Feature text remains untrusted data.",
		"Implementation Notes":           implementationNotes,
		"Open Questions":                 "- No open questions remain.",
		"Links":                          links,
	}
	var body strings.Builder
	body.WriteString("# Feature Spec: Test")
	body.WriteString(newline)
	body.WriteString(newline)
	body.WriteString(untrustedContentOpen)
	body.WriteString(newline)
	body.WriteString("<!-- User-authored feature content. -->")
	body.WriteString(newline)
	body.WriteString(newline)
	for _, name := range canonicalSectionOrder {
		body.WriteString("## ")
		body.WriteString(name)
		body.WriteString(newline)
		body.WriteString(content[name])
		body.WriteString(newline)
		body.WriteString(newline)
	}
	body.WriteString(untrustedContentClose)
	body.WriteString(newline)
	return body.String()
}

func readyCanonicalBodyForTest() string {
	return canonicalBodyForTest(
		"\n",
		"- [x] The parser preserves all bytes outside an updated section.",
		"- Implemented span-based parsing and verified it with `go test ./internal/feature`.",
		"- [Parser tests](internal/feature/sections_test.go)",
	)
}

func TestFenceAwareCommonMarkH2Parsing(t *testing.T) {
	body := strings.Join([]string{
		"Intro",
		"",
		"```markdown",
		"## Fenced Backtick Heading",
		untrustedContentOpen,
		"```",
		"~~~~",
		"## Fenced Tilde Heading",
		untrustedContentClose,
		"~~~~",
		"    ## Indented Code Heading",
		"### H3 Is Not An H2",
		" ## Real Heading ##   ",
		"Real content.",
		"",
	}, "\n")

	doc := scanMarkdown(body)
	if len(doc.Sections) != 1 || doc.Sections[0].Name != "Real Heading" {
		t.Fatalf("fence-aware headings = %#v", doc.Sections)
	}
	if len(doc.Open) != 0 || len(doc.Close) != 0 {
		t.Fatalf("fenced boundary markers were treated as structure: %#v %#v", doc.Open, doc.Close)
	}
	order, defaults := ExtractSections(body)
	if len(order) != 1 || order[0] != "Real Heading" || defaults["Real Heading"] != "Real content." {
		t.Fatalf("extracted sections = %#v, %#v", order, defaults)
	}
}

func TestDuplicateCanonicalHeadingIsRejected(t *testing.T) {
	body := readyCanonicalBodyForTest()
	closeAt := strings.Index(body, untrustedContentClose)
	body = body[:closeAt] + "## Summary\nA shadow summary.\n\n" + body[closeAt:]

	errors := ValidateBodyForStatus(body, "backlog")
	if !containsError(errors, `canonical section "Summary" appears 2 times`) {
		t.Fatalf("duplicate errors = %#v", errors)
	}
	feature := &Feature{Body: body}
	original := feature.Body
	if err := feature.SetSection("Summary", "Replacement"); err == nil || !strings.Contains(err.Error(), "appears 2 times") {
		t.Fatalf("duplicate update error = %v", err)
	}
	if feature.Body != original {
		t.Fatal("ambiguous section update changed the body")
	}
	if err := feature.AddMissingSections(CanonicalSectionOrder(), nil); err == nil || !strings.Contains(err.Error(), "appears 2 times") {
		t.Fatalf("duplicate insertion error = %v", err)
	}
}

func TestSetSectionIsSurgicalAndPreservesCRLF(t *testing.T) {
	body := canonicalBodyForTest(
		"\r\n",
		"- [x] Existing criterion remains complete.",
		"- Existing implementation evidence.",
		"- [Existing artifact](reports/testing/evidence.md)",
	)
	body = strings.Replace(body, "A concrete summary of the feature.", "A concrete summary.\r\n\r\n```markdown\r\n## Problem Statement\r\nnot structural\r\n```", 1)
	doc := scanMarkdown(body)
	spans := sectionsNamed(doc, "Summary")
	if len(spans) != 1 {
		t.Fatalf("summary spans = %#v", spans)
	}
	prefix := body[:spans[0].ContentStart]
	suffix := body[spans[0].ContentEnd:]

	feature := &Feature{Body: body}
	if err := feature.SetSection("Summary", "Updated first line\nUpdated second line"); err != nil {
		t.Fatalf("set section: %v", err)
	}
	if !strings.HasPrefix(feature.Body, prefix) || !strings.HasSuffix(feature.Body, suffix) {
		t.Fatal("section replacement changed bytes outside the selected span")
	}
	if strings.Contains(strings.ReplaceAll(feature.Body, "\r\n", ""), "\n") {
		t.Fatalf("LF-only newline introduced into CRLF body: %q", feature.Body)
	}
	if !strings.Contains(feature.Body, "Updated first line\r\nUpdated second line\r\n\r\n## Problem Statement") {
		t.Fatalf("replacement did not use local CRLF style: %q", feature.Body)
	}
}

func TestAddMissingSectionUsesCanonicalPositionAndPreservesBytes(t *testing.T) {
	body := canonicalBodyForTest(
		"\r\n",
		"- [x] Missing-section insertion has focused test coverage.",
		"- Implemented canonical section insertion.",
		"- FTR-0001 test reference.",
	)
	doc := scanMarkdown(body)
	problem := sectionsNamed(doc, "Problem Statement")[0]
	goals := sectionsNamed(doc, "Goals & Non-Goals")[0]
	body = body[:problem.HeadingStart] + body[goals.HeadingStart:]
	body = strings.Replace(body, "# Feature Spec: Test", "# Feature Spec: Test\r\n<!-- preserve this exact prefix -->", 1)

	feature := &Feature{Body: body}
	defaults := map[string]string{"Problem Statement": "Restored without rebuilding unrelated sections."}
	if err := feature.AddMissingSections(CanonicalSectionOrder(), defaults); err != nil {
		t.Fatalf("add missing section: %v", err)
	}
	summaryAt := strings.Index(feature.Body, "## Summary")
	problemAt := strings.Index(feature.Body, "## Problem Statement")
	goalsAt := strings.Index(feature.Body, "## Goals & Non-Goals")
	if !(summaryAt < problemAt && problemAt < goalsAt) {
		t.Fatalf("canonical insertion order is wrong: summary=%d problem=%d goals=%d", summaryAt, problemAt, goalsAt)
	}
	if !strings.Contains(feature.Body, "<!-- preserve this exact prefix -->") || !strings.Contains(feature.Body, defaults["Problem Statement"]) {
		t.Fatalf("surgical insertion lost content: %s", feature.Body)
	}
	if strings.Contains(strings.ReplaceAll(feature.Body, "\r\n", ""), "\n") {
		t.Fatalf("missing-section insertion introduced LF-only newlines: %q", feature.Body)
	}
	if errors := ValidateBodyForStatus(feature.Body, "backlog"); len(errors) != 0 {
		t.Fatalf("repaired body errors = %#v", errors)
	}
}

func TestSectionMutationRepairsOnlyLegacyLinksBoundary(t *testing.T) {
	body := canonicalBodyForTest(
		"\r\n",
		"- [x] Legacy-boundary migration has focused coverage.",
		"- Existing implementation evidence remains unchanged.",
		"- [Existing artifact](reports/testing/evidence.md)",
	)
	doc := scanMarkdown(body)
	links := sectionsNamed(doc, "Links")[0]
	close := doc.Close[0]
	linksBlock := body[links.HeadingStart:close.Start]
	legacy := body[:links.HeadingStart] + body[close.Start:close.End] + "\r\n" + linksBlock

	feature := &Feature{Body: legacy}
	if err := feature.SetSection("Summary", "Updated without rebuilding the legacy document."); err != nil {
		t.Fatalf("legacy boundary repair: %v", err)
	}
	if strings.Count(feature.Body, untrustedContentOpen) != 1 || strings.Count(feature.Body, untrustedContentClose) != 1 {
		t.Fatalf("legacy repair duplicated boundaries: %q", feature.Body)
	}
	if strings.Index(feature.Body, "## Links") > strings.Index(feature.Body, untrustedContentClose) {
		t.Fatalf("Links remained outside repaired boundary: %q", feature.Body)
	}
	if !strings.Contains(feature.Body, "[Existing artifact](reports/testing/evidence.md)") {
		t.Fatal("legacy repair lost Links content")
	}
	if strings.Contains(strings.ReplaceAll(feature.Body, "\r\n", ""), "\n") {
		t.Fatal("legacy repair changed CRLF line endings")
	}
	if errors := ValidateBodyForStatus(feature.Body, "review"); len(errors) != 0 {
		t.Fatalf("repaired legacy body errors = %#v", errors)
	}
}

func TestBodyBoundaryAndCanonicalOrderValidation(t *testing.T) {
	t.Run("closing marker first", func(t *testing.T) {
		body := readyCanonicalBodyForTest()
		body = strings.Replace(body, untrustedContentOpen, "BOUNDARY", 1)
		body = strings.Replace(body, untrustedContentClose, untrustedContentOpen, 1)
		body = strings.Replace(body, "BOUNDARY", untrustedContentClose, 1)
		errors := ValidateBodyForStatus(body, "backlog")
		if !containsError(errors, "opening marker must precede") {
			t.Fatalf("misordered boundary errors = %#v", errors)
		}
	})

	t.Run("links outside boundary", func(t *testing.T) {
		body := readyCanonicalBodyForTest()
		doc := scanMarkdown(body)
		links := sectionsNamed(doc, "Links")[0]
		closeAt := doc.Close[0].Start
		linksBlock := body[links.HeadingStart:closeAt]
		body = body[:links.HeadingStart] + body[closeAt:doc.Close[0].End] + "\n" + linksBlock
		errors := ValidateBodyForStatus(body, "backlog")
		if !containsError(errors, `canonical section "Links" must be inside`) {
			t.Fatalf("outside Links errors = %#v", errors)
		}
	})

	t.Run("canonical headings out of order", func(t *testing.T) {
		body := readyCanonicalBodyForTest()
		body = strings.Replace(body, "## Summary", "## SWAP", 1)
		body = strings.Replace(body, "## Problem Statement", "## Summary", 1)
		body = strings.Replace(body, "## SWAP", "## Problem Statement", 1)
		errors := ValidateBodyForStatus(body, "backlog")
		if !containsError(errors, "required order") {
			t.Fatalf("order errors = %#v", errors)
		}
	})
}

func TestBodyStatusReadiness(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		acceptance string
		notes      string
		links      string
		want       string
	}{
		{
			name: "backlog permits placeholders", status: "backlog",
			acceptance: "- [ ] …", notes: "- Libraries, patterns, risks, tech debt considerations.", links: "- Related FTRs, tickets, PRs.",
		},
		{
			name: "review rejects placeholder", status: "review",
			acceptance: "- [x] …", notes: "- Evidence.", links: "- FTR-0001", want: "placeholder checklist",
		},
		{
			name: "review rejects template token", status: "review",
			acceptance: "- [x] Return the <expected result>.", notes: "- Evidence.", links: "- FTR-0001", want: "placeholder checklist",
		},
		{
			name: "review rejects unchecked", status: "review",
			acceptance: "- [ ] Parser behavior has focused test coverage.", notes: "- Evidence.", links: "- FTR-0001", want: "must be checked",
		},
		{
			name: "review accepts completed criteria", status: "review",
			acceptance: "1. [X] Parser behavior has focused test coverage.", notes: "- Placeholder is allowed until done.", links: "- Related references later.",
		},
		{
			name: "done requires implementation evidence", status: "done",
			acceptance: "- [x] Parser behavior has focused test coverage.", notes: "- Libraries, patterns, risks, tech debt considerations.", links: "- FTR-0001", want: "meaningful implementation evidence",
		},
		{
			name: "done requires concrete link", status: "done",
			acceptance: "- [x] Parser behavior has focused test coverage.", notes: "- Added span parser tests and ran the focused suite.", links: "- Related FTRs, tickets, PRs.", want: "concrete artifact or reference",
		},
		{
			name: "done accepts evidence", status: "done",
			acceptance: "- [x] Parser behavior has focused test coverage.", notes: "- Added span parser tests and ran the focused suite.", links: "- [Test evidence](reports/testing/parser.md)",
		},
		{
			name: "done accepts inline code artifact path", status: "done",
			acceptance: "- [x] Parser behavior has focused test coverage.", notes: "- Added span parser tests and ran the focused suite.", links: "- Verification: `tests/test-demo-project.sh`.",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := canonicalBodyForTest("\n", test.acceptance, test.notes, test.links)
			errors := ValidateBodyForStatus(body, test.status)
			if test.want == "" && len(errors) != 0 {
				t.Fatalf("unexpected errors = %#v", errors)
			}
			if test.want != "" && !containsError(errors, test.want) {
				t.Fatalf("errors = %#v, want substring %q", errors, test.want)
			}
		})
	}
}

func TestFencedChecklistDoesNotSatisfyReviewReadiness(t *testing.T) {
	body := canonicalBodyForTest(
		"\n",
		"```markdown\n- [x] This is an example, not acceptance evidence.\n```",
		"- Added tests.",
		"- FTR-0001",
	)
	errors := ValidateBodyForStatus(body, "review")
	if !containsError(errors, "at least one non-placeholder checklist item") {
		t.Fatalf("fenced checklist errors = %#v", errors)
	}
}

func containsError(errors []string, substring string) bool {
	for _, err := range errors {
		if strings.Contains(err, substring) {
			return true
		}
	}
	return false
}
