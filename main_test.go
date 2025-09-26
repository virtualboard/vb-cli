package main

import (
	"bytes"
	"io"
	"testing"

	"github.com/virtualboard/vb-cli/cmd"
	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestRunSuccessAndFailure(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)
	if _, err := mgr.CreateFeature("Main Test Feature", nil); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	root := cmd.RootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--root", fix.Root, "index", "--format", "json", "--output", "-"})
	if code := run(); code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	root.SetArgs([]string{"--root", fix.Root, "index", "--format", "invalid"})
	if code := run(); code != cmd.ExitCodeValidation {
		t.Fatalf("expected validation exit, got %d", code)
	}

	root.SetArgs(nil)
	config.SetCurrent(nil)
}
