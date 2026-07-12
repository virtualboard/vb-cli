package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/virtualboard/vb-cli/internal/testutil"
	"github.com/virtualboard/vb-cli/internal/upgrade"
	"github.com/virtualboard/vb-cli/internal/version"
)

func TestNewUpgradeCommand(t *testing.T) {
	command := newUpgradeCommand()
	if command.Use != "upgrade" || command.Short != "Upgrade vb to the latest version" {
		t.Fatalf("unexpected upgrade command metadata: %s / %s", command.Use, command.Short)
	}
	if !strings.Contains(command.Long, "Check for a newer version") || command.RunE == nil {
		t.Fatalf("upgrade command is missing its description or runner")
	}
}

func stubUpgrade(t *testing.T, fn func(*logrus.Logger, string) (*upgrade.UpgradeResult, error)) {
	t.Helper()
	original := runUpgrade
	runUpgrade = fn
	t.Cleanup(func() { runUpgrade = original })
}

func stubUpgradeCheck(t *testing.T, fn func(*logrus.Logger, string) (*upgrade.UpgradeResult, error)) {
	t.Helper()
	original := runUpgradeCheck
	runUpgradeCheck = fn
	t.Cleanup(func() { runUpgradeCheck = original })
}

func executeUpgradeCommand(t *testing.T, jsonOutput bool) (string, error) {
	return executeUpgradeCommandWithDryRun(t, jsonOutput, false)
}

func executeUpgradeCommandWithDryRun(t *testing.T, jsonOutput, dryRun bool) (string, error) {
	t.Helper()
	fix := testutil.NewFixture(t)
	_, output := setupOptions(t, fix, jsonOutput, false, dryRun)
	command := newUpgradeCommand()
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetOut(output)
	command.SetErr(output)
	return executeCommand(command, output)
}

func TestUpgradeCommandDryRunChecksWithoutReplacing(t *testing.T) {
	fullUpgradeCalled := false
	stubUpgrade(t, func(_ *logrus.Logger, _ string) (*upgrade.UpgradeResult, error) {
		fullUpgradeCalled = true
		return nil, errors.New("full upgrade must not run")
	})
	stubUpgradeCheck(t, func(_ *logrus.Logger, current string) (*upgrade.UpgradeResult, error) {
		return &upgrade.UpgradeResult{
			Message:         "Update available; no files changed",
			CurrentVersion:  current,
			LatestVersion:   "v9.9.9",
			UpdateAvailable: true,
		}, nil
	})
	output, err := executeUpgradeCommandWithDryRun(t, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if fullUpgradeCalled {
		t.Fatal("dry-run invoked binary replacement path")
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			DryRun          bool `json:"dry_run"`
			Upgraded        bool `json:"upgraded"`
			UpdateAvailable bool `json:"update_available"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse dry-run JSON: %v\n%s", err, output)
	}
	if !payload.Success || !payload.Data.DryRun || payload.Data.Upgraded || !payload.Data.UpdateAvailable {
		t.Fatalf("unexpected dry-run payload: %+v", payload)
	}
}

func executeCommand(command interface {
	Execute() error
}, output *bytes.Buffer) (string, error) {
	err := command.Execute()
	return output.String(), err
}

func TestUpgradeCommandTextSuccess(t *testing.T) {
	stubUpgrade(t, func(_ *logrus.Logger, current string) (*upgrade.UpgradeResult, error) {
		if current != version.Current {
			t.Fatalf("current version = %s", current)
		}
		return &upgrade.UpgradeResult{
			Message:        "Already current",
			CurrentVersion: current,
			LatestVersion:  current,
		}, nil
	})
	output, err := executeUpgradeCommand(t, false)
	if err != nil {
		t.Fatal(err)
	}
	if output != "Already current\n" {
		t.Fatalf("unexpected text output %q", output)
	}
}

func TestUpgradeCommandJSONSuccess(t *testing.T) {
	stubUpgrade(t, func(_ *logrus.Logger, current string) (*upgrade.UpgradeResult, error) {
		return &upgrade.UpgradeResult{
			Message:        "Upgraded",
			CurrentVersion: current,
			LatestVersion:  "v9.9.9",
			Upgraded:       true,
		}, nil
	})
	output, err := executeUpgradeCommand(t, true)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Upgraded bool   `json:"upgraded"`
			Latest   string `json:"latest_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("parse JSON output: %v\n%s", err, output)
	}
	if !payload.Success || !payload.Data.Upgraded || payload.Data.Latest != "v9.9.9" {
		t.Fatalf("unexpected JSON payload: %+v", payload)
	}
}

func TestUpgradeCommandJSONFailureReturnsNonzero(t *testing.T) {
	stubUpgrade(t, func(_ *logrus.Logger, _ string) (*upgrade.UpgradeResult, error) {
		return nil, errors.New("remote release rejected")
	})
	output, err := executeUpgradeCommand(t, true)
	if err == nil || ExitCode(err) != ExitCodeExternalCommand {
		t.Fatalf("JSON failure exit = %d, err = %v", ExitCode(err), err)
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Error string `json:"error"`
		} `json:"data"`
	}
	if jsonErr := json.Unmarshal([]byte(output), &payload); jsonErr != nil {
		t.Fatalf("parse JSON failure: %v\n%s", jsonErr, output)
	}
	if payload.Success || !strings.Contains(payload.Data.Error, "remote release rejected") {
		t.Fatalf("unexpected JSON failure payload: %+v", payload)
	}
	if strings.Contains(strings.ToLower(output+err.Error()), "sudo") {
		t.Fatalf("upgrade error recommended unsafe sudo retry: %s / %v", output, err)
	}
}

func TestUpgradeCommandPermissionFailureDoesNotRecommendSudo(t *testing.T) {
	stubUpgrade(t, func(_ *logrus.Logger, _ string) (*upgrade.UpgradeResult, error) {
		return nil, fs.ErrPermission
	})
	output, err := executeUpgradeCommand(t, false)
	if err == nil || ExitCode(err) != ExitCodeFilesystem {
		t.Fatalf("permission failure exit = %d, err = %v", ExitCode(err), err)
	}
	if strings.Contains(strings.ToLower(output+err.Error()), "sudo") {
		t.Fatalf("permission failure recommended unsafe sudo retry: %s / %v", output, err)
	}
}
