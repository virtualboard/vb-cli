package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUpgradeCommand(t *testing.T) {
	cmd := newUpgradeCommand()

	assert.Equal(t, "upgrade", cmd.Use)
	assert.Equal(t, "Upgrade vb to the latest version", cmd.Short)
	assert.Contains(t, cmd.Long, "Check for a newer version")
	assert.NotNil(t, cmd.RunE)
}

func TestUpgradeCommandRunE(t *testing.T) {
	// This test requires network access and GitHub API
	// Skip if running in CI or if network is not available
	if os.Getenv("CI") != "" || os.Getenv("SKIP_NETWORK_TESTS") != "" {
		t.Skip("Skipping network-dependent test")
	}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "upgrade-cmd-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Change to the temporary directory
	oldDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(oldDir)

	err = os.Chdir(tempDir)
	require.NoError(t, err)

	// Create a minimal config file
	configContent := `root: .
json: false
verbose: false
dry_run: false
log_file: ""
`
	err = os.WriteFile(".vb.yaml", []byte(configContent), 0644)
	require.NoError(t, err)

	// Create the command
	cmd := newUpgradeCommand()

	// Set up output buffer
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	// Run the command
	err = cmd.Execute()

	// The command might succeed (if no update available) or fail (if network issues)
	// We just want to make sure it doesn't panic and handles errors gracefully
	if err != nil {
		// Check that the error is related to upgrade functionality or configuration
		assert.True(t, strings.Contains(err.Error(), "upgrade") || strings.Contains(err.Error(), "configuration"))
	}
}

func TestUpgradeCommandJSONOutput(t *testing.T) {
	// This test requires network access and GitHub API
	if os.Getenv("CI") != "" || os.Getenv("SKIP_NETWORK_TESTS") != "" {
		t.Skip("Skipping network-dependent test")
	}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "upgrade-cmd-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Change to the temporary directory
	oldDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(oldDir)

	err = os.Chdir(tempDir)
	require.NoError(t, err)

	// Create a config file with JSON output enabled
	configContent := `root: .
json: true
verbose: false
dry_run: false
log_file: ""
`
	err = os.WriteFile(".vb.yaml", []byte(configContent), 0644)
	require.NoError(t, err)

	// Create the command
	cmd := newUpgradeCommand()

	// Set up output buffer
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	// Run the command
	err = cmd.Execute()

	// Check that the output is JSON format
	output := buf.String()
	if err == nil {
		// If successful, should be JSON
		assert.Contains(t, output, "{")
		assert.Contains(t, output, "}")
	} else {
		// If error, should also be JSON or contain configuration error
		assert.True(t, strings.Contains(output, "{") || strings.Contains(err.Error(), "configuration"))
	}
}

func TestUpgradeCommandWithVerboseLogging(t *testing.T) {
	// This test requires network access and GitHub API
	if os.Getenv("CI") != "" || os.Getenv("SKIP_NETWORK_TESTS") != "" {
		t.Skip("Skipping network-dependent test")
	}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "upgrade-cmd-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Change to the temporary directory
	oldDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(oldDir)

	err = os.Chdir(tempDir)
	require.NoError(t, err)

	// Create a config file with verbose logging
	configContent := `root: .
json: false
verbose: true
dry_run: false
log_file: ""
`
	err = os.WriteFile(".vb.yaml", []byte(configContent), 0644)
	require.NoError(t, err)

	// Create the command
	cmd := newUpgradeCommand()

	// Set up output buffer
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	// Run the command
	err = cmd.Execute()

	// The command should run without panicking
	// We don't assert specific output since it depends on actual GitHub releases
	if err != nil {
		assert.True(t, strings.Contains(err.Error(), "upgrade") || strings.Contains(err.Error(), "configuration"))
	}
}

func TestUpgradeCommandIntegration(t *testing.T) {
	// This is an integration test that tests the full command flow
	// Skip if running in CI or if network is not available
	if os.Getenv("CI") != "" || os.Getenv("SKIP_NETWORK_TESTS") != "" {
		t.Skip("Skipping network-dependent integration test")
	}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "upgrade-integration-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Change to the temporary directory
	oldDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(oldDir)

	err = os.Chdir(tempDir)
	require.NoError(t, err)

	// Create a config file
	configContent := `root: .
json: false
verbose: true
dry_run: false
log_file: ""
`
	err = os.WriteFile(".vb.yaml", []byte(configContent), 0644)
	require.NoError(t, err)

	// Test the command through the root command
	rootCmd := RootCommand()
	rootCmd.SetArgs([]string{"upgrade"})

	// Set up output buffer
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)

	// Run the command
	err = rootCmd.Execute()

	// The command should run without panicking
	// We don't assert specific output since it depends on actual GitHub releases
	if err != nil {
		assert.True(t, strings.Contains(err.Error(), "upgrade") || strings.Contains(err.Error(), "configuration"))
	}
}
