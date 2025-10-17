package feature

import (
	"strings"
	"testing"
)

func TestInvalidFileError(t *testing.T) {
	// Test empty error
	emptyErr := &InvalidFileError{Files: []InvalidFile{}}
	if emptyErr.Error() != "no invalid files" {
		t.Fatalf("expected 'no invalid files', got: %s", emptyErr.Error())
	}

	// Test single file error
	singleErr := &InvalidFileError{
		Files: []InvalidFile{
			{Path: "/path/to/file.md", Reason: "missing frontmatter"},
		},
	}
	msg := singleErr.Error()
	if !strings.Contains(msg, "/path/to/file.md") {
		t.Fatalf("expected error to contain file path, got: %s", msg)
	}
	if !strings.Contains(msg, "missing frontmatter") {
		t.Fatalf("expected error to contain reason, got: %s", msg)
	}

	// Test multiple file errors
	multiErr := &InvalidFileError{
		Files: []InvalidFile{
			{Path: "/path/to/file1.md", Reason: "missing frontmatter"},
			{Path: "/path/to/file2.md", Reason: "invalid yaml"},
		},
	}
	msg = multiErr.Error()
	if !strings.Contains(msg, "found 2 invalid feature files") {
		t.Fatalf("expected error to mention count, got: %s", msg)
	}
	if !strings.Contains(msg, "/path/to/file1.md") || !strings.Contains(msg, "/path/to/file2.md") {
		t.Fatalf("expected error to list both files, got: %s", msg)
	}
	if !strings.Contains(msg, "missing frontmatter") || !strings.Contains(msg, "invalid yaml") {
		t.Fatalf("expected error to list both reasons, got: %s", msg)
	}
	if !strings.Contains(msg, "review and move") {
		t.Fatalf("expected error to include guidance, got: %s", msg)
	}
}
