package feature

import "testing"

const body = `Intro line

## First

One

## Second

Two
`

func TestParseAndRebuildSections(t *testing.T) {
	sections := parseSections(body)
	if sections.Intro != "Intro line" {
		t.Fatalf("unexpected intro: %q", sections.Intro)
	}
	if len(sections.Order) != 2 {
		t.Fatalf("expected two sections, got %d", len(sections.Order))
	}
	sections.Data["First"] = "Updated"
	rebuilt := rebuildBody(sections)
	if len(rebuilt) == 0 || rebuilt == body {
		t.Fatalf("expected body to change")
	}

	order, defaults := ExtractSections(body)
	if len(order) != 2 || defaults["Second"] != "Two" {
		t.Fatalf("unexpected extract output: %v %v", order, defaults)
	}
}
