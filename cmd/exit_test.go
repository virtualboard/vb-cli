package cmd

import "testing"

func TestCLIErrorAndExitCode(t *testing.T) {
	if ExitCode(nil) != ExitCodeSuccess {
		t.Fatalf("expected success exit code")
	}

	err := NewCLIError(ExitCodeValidation, "invalid")
	if ExitCode(err) != ExitCodeValidation {
		t.Fatalf("unexpected exit code")
	}

	wrapped := WrapCLIError(ExitCodeDependency, err)
	if ExitCode(wrapped) != ExitCodeDependency {
		t.Fatalf("wrap should use provided code")
	}

	zeroCode := &CLIError{Code: 0}
	if ExitCode(zeroCode) != ExitCodeUnknown {
		t.Fatalf("expected unknown for zero code")
	}

	if WrapCLIError(ExitCodeDependency, nil) != nil {
		t.Fatalf("wrap nil should return nil")
	}
}
