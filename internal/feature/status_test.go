package feature

import "testing"

func TestDirectoryForStatus(t *testing.T) {
	if DirectoryForStatus("backlog") != "features/backlog" {
		t.Fatalf("unexpected directory for backlog")
	}
	if DirectoryForStatus("unknown") != "" {
		t.Fatalf("expected empty directory for unknown status")
	}
}

func TestValidStatusesAndValidation(t *testing.T) {
	statuses := ValidStatuses()
	if len(statuses) != len(statusDirectories) {
		t.Fatalf("expected %d statuses", len(statusDirectories))
	}
	if err := ValidateStatus("backlog"); err != nil {
		t.Fatalf("validate status failed: %v", err)
	}
	if err := ValidateStatus("nope"); err == nil {
		t.Fatalf("expected error for invalid status")
	}
}

func TestValidateTransition(t *testing.T) {
	if err := ValidateTransition("backlog", "in-progress"); err != nil {
		t.Fatalf("expected transition to succeed: %v", err)
	}
	if err := ValidateTransition("done", "backlog"); err == nil {
		t.Fatalf("expected transition error")
	}
	if err := ValidateTransition("unknown", "backlog"); err == nil {
		t.Fatalf("expected error for unknown current status")
	}
}
