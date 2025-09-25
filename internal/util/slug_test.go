package util

import "testing"

func TestSlugify(t *testing.T) {
	if got := Slugify("Hello World!"); got != "hello-world" {
		t.Fatalf("unexpected slug: %s", got)
	}
	if got := Slugify("   "); got != "feature" {
		t.Fatalf("expected fallback slug, got %s", got)
	}
	if got := Slugify("Already-Slug"); got != "already-slug" {
		t.Fatalf("unexpected slug: %s", got)
	}
}
