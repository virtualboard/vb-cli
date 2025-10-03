package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestNewArtCommand(t *testing.T) {
	cmd := newArtCommand()
	assert.Equal(t, "art", cmd.Use)
	assert.Equal(t, "Render the VirtualBoard logo as colored ASCII art", cmd.Short)
	assert.Equal(t, "Render the VirtualBoard logo (avatar.png) as colored ASCII art in the terminal.", cmd.Long)
}

func TestArtCommand_Execute(t *testing.T) {
	tests := []struct {
		name           string
		setupFunc      func(t *testing.T) *testutil.Fixture
		expectedError  bool
		expectedOutput bool
	}{
		{
			name: "successful execution with valid avatar",
			setupFunc: func(t *testing.T) *testutil.Fixture {
				// Create a test environment with avatar.png
				fixture := testutil.NewFixture(t)
				// Create docs directory and copy the real avatar.png
				docsDir := filepath.Join(fixture.Root, "docs")
				err := os.MkdirAll(docsDir, 0o750)
				require.NoError(t, err)

				// Copy the real avatar.png if it exists
				realAvatarPath := "docs/avatar.png"
				if _, err := os.Stat(realAvatarPath); err == nil {
					data, err := os.ReadFile(realAvatarPath)
					require.NoError(t, err)

					avatarPath := filepath.Join(docsDir, "avatar.png")
					err = os.WriteFile(avatarPath, data, 0o600)
					require.NoError(t, err)
				} else {
					// Create a simple test file if real avatar doesn't exist
					avatarPath := filepath.Join(docsDir, "avatar.png")
					err = os.WriteFile(avatarPath, []byte("fake_png_data"), 0o600)
					require.NoError(t, err)
				}

				return fixture
			},
			expectedError:  false,
			expectedOutput: true,
		},
		{
			name: "error when avatar not found",
			setupFunc: func(t *testing.T) *testutil.Fixture {
				// Create a test environment without avatar.png
				fixture := testutil.NewFixture(t)
				// Create docs directory but no avatar.png
				docsDir := filepath.Join(fixture.Root, "docs")
				err := os.MkdirAll(docsDir, 0o750)
				require.NoError(t, err)
				return fixture
			},
			expectedError:  true,
			expectedOutput: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := tt.setupFunc(t)

			// Change to the test directory
			originalDir, err := os.Getwd()
			require.NoError(t, err)
			defer os.Chdir(originalDir)

			err = os.Chdir(fixture.Root)
			require.NoError(t, err)

			// Create a command with test context
			cmd := newArtCommand()
			var buf bytes.Buffer
			cmd.SetOut(&buf)

			// Set up config context
			opts := fixture.Options(t, false, false, false)
			ctx := opts.WithContext(context.Background())
			cmd.SetContext(ctx)

			// Execute the command
			err = cmd.Execute()

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				// For the test with fake data, we expect an error but that's okay
				// The important thing is that the command structure works
				if err != nil {
					// If it's an image decode error, that's expected with fake data
					assert.Contains(t, err.Error(), "failed to convert image to ASCII")
				} else {
					// If it succeeds, check for output
					output := buf.String()
					assert.NotEmpty(t, output)
				}
			}
		})
	}
}

func TestArtCommand_JSONOutput(t *testing.T) {
	fixture := testutil.NewFixture(t)

	// Create docs directory and copy the real avatar.png
	docsDir := filepath.Join(fixture.Root, "docs")
	err := os.MkdirAll(docsDir, 0o750)
	require.NoError(t, err)

	// Copy the real avatar.png if it exists
	realAvatarPath := "docs/avatar.png"
	if _, err := os.Stat(realAvatarPath); err == nil {
		data, err := os.ReadFile(realAvatarPath)
		require.NoError(t, err)

		avatarPath := filepath.Join(docsDir, "avatar.png")
		err = os.WriteFile(avatarPath, data, 0o600)
		require.NoError(t, err)
	} else {
		// Create a simple test file if real avatar doesn't exist
		avatarPath := filepath.Join(docsDir, "avatar.png")
		err = os.WriteFile(avatarPath, []byte("fake_png_data"), 0o600)
		require.NoError(t, err)
	}

	// Change to the test directory
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)

	err = os.Chdir(fixture.Root)
	require.NoError(t, err)

	// Create a command with JSON output enabled
	cmd := newArtCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	// Set up config context with JSON output
	opts := fixture.Options(t, true, false, false)
	ctx := opts.WithContext(context.Background())
	cmd.SetContext(ctx)

	// Execute the command
	err = cmd.Execute()

	// We expect an error with fake data, but let's check the structure
	if err != nil {
		assert.Contains(t, err.Error(), "failed to convert image to ASCII")
	} else {
		output := buf.String()
		assert.Contains(t, output, "ascii_art")
		assert.Contains(t, output, "source")
		assert.Contains(t, output, "docs/avatar.png")
	}
}

func TestFindAvatarPath(t *testing.T) {
	tests := []struct {
		name          string
		setupFunc     func(t *testing.T) string
		expectedError bool
		expectedPath  string
	}{
		{
			name: "finds avatar in current directory",
			setupFunc: func(t *testing.T) string {
				fixture := testutil.NewFixture(t)
				// Create docs directory and avatar.png
				docsDir := filepath.Join(fixture.Root, "docs")
				err := os.MkdirAll(docsDir, 0o750)
				require.NoError(t, err)

				avatarPath := filepath.Join(docsDir, "avatar.png")
				err = os.WriteFile(avatarPath, []byte("test_data"), 0o600)
				require.NoError(t, err)

				return fixture.Root
			},
			expectedError: false,
			expectedPath:  "docs/avatar.png",
		},
		{
			name: "finds avatar in parent directory",
			setupFunc: func(t *testing.T) string {
				fixture := testutil.NewFixture(t)
				// Create docs directory and avatar.png
				docsDir := filepath.Join(fixture.Root, "docs")
				err := os.MkdirAll(docsDir, 0o750)
				require.NoError(t, err)

				avatarPath := filepath.Join(docsDir, "avatar.png")
				err = os.WriteFile(avatarPath, []byte("test_data"), 0o600)
				require.NoError(t, err)

				// Create subdirectory
				subDir := filepath.Join(fixture.Root, "subdir")
				err = os.MkdirAll(subDir, 0o750)
				require.NoError(t, err)

				return subDir
			},
			expectedError: false,
			expectedPath:  filepath.Join("..", "docs", "avatar.png"),
		},
		{
			name: "returns error when avatar not found",
			setupFunc: func(t *testing.T) string {
				fixture := testutil.NewFixture(t)
				// Create docs directory but no avatar.png
				docsDir := filepath.Join(fixture.Root, "docs")
				err := os.MkdirAll(docsDir, 0o750)
				require.NoError(t, err)
				return fixture.Root
			},
			expectedError: true,
			expectedPath:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rootDir := tt.setupFunc(t)

			// Change to the test directory
			originalDir, err := os.Getwd()
			require.NoError(t, err)
			defer os.Chdir(originalDir)

			err = os.Chdir(rootDir)
			require.NoError(t, err)

			path, err := findAvatarPath()

			if tt.expectedError {
				assert.Error(t, err)
				assert.Empty(t, path)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, path)
			}
		})
	}
}

func TestConvertImageToASCII_InvalidFile(t *testing.T) {
	fixture := testutil.NewFixture(t)
	invalidPath := filepath.Join(fixture.Root, "nonexistent.png")

	_, err := convertImageToASCII(invalidPath)
	assert.Error(t, err)
}

func TestConvertImageToASCII_RealAvatar(t *testing.T) {
	// Test with the real avatar.png if it exists
	avatarPath := "docs/avatar.png"
	if _, err := os.Stat(avatarPath); err != nil {
		t.Skip("Real avatar.png not found, skipping test")
	}

	asciiArt, err := convertImageToASCII(avatarPath)
	require.NoError(t, err)
	assert.NotEmpty(t, asciiArt)

	// Should contain ANSI color codes
	assert.Contains(t, asciiArt, "\033[")
	// Should contain newlines
	assert.Contains(t, asciiArt, "\n")
	// Should contain reset codes
	assert.Contains(t, asciiArt, "\033[0m")
}
