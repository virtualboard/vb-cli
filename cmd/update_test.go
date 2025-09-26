package cmd

import "testing"

func TestSplitPair(t *testing.T) {
	key, value, err := splitPair("key=value")
	if err != nil || key != "key" || value != "value" {
		t.Fatalf("unexpected split: %s %s %v", key, value, err)
	}
	if _, _, err := splitPair("novalue"); err == nil {
		t.Fatalf("expected error for missing delimiter")
	}
	if _, _, err := splitPair("=value"); err == nil {
		t.Fatalf("expected error for empty key")
	}
}
