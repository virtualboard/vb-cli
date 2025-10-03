package cmd

import (
	"fmt"
	"image"
	_ "image/png"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newArtCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "art",
		Short: "Render the VirtualBoard logo as colored ASCII art",
		Long:  "Render the VirtualBoard logo (avatar.png) as colored ASCII art in the terminal.",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}

			// Find the avatar.png file
			avatarPath, err := findAvatarPath()
			if err != nil {
				return fmt.Errorf("failed to find avatar.png: %w", err)
			}

			// Load and process the image
			asciiArt, err := convertImageToASCII(avatarPath)
			if err != nil {
				return fmt.Errorf("failed to convert image to ASCII: %w", err)
			}

			if opts.JSONOutput {
				payload := map[string]interface{}{
					"ascii_art": asciiArt,
					"source":    avatarPath,
				}
				return respond(cmd, opts, true, "", payload)
			}

			// Output the ASCII art
			fmt.Fprint(cmd.OutOrStdout(), asciiArt)
			return nil
		},
	}
}

// findAvatarPath locates the avatar.png file in the docs directory
func findAvatarPath() (string, error) {
	// Try relative to current directory first
	avatarPath := "docs/avatar.png"
	if _, err := os.Stat(avatarPath); err == nil {
		return avatarPath, nil
	}

	// Try to find it in the project root
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Walk up the directory tree to find the project root
	currentDir := wd
	for {
		testPath := filepath.Join(currentDir, "docs", "avatar.png")
		if _, err := os.Stat(testPath); err == nil {
			return testPath, nil
		}

		parent := filepath.Dir(currentDir)
		if parent == currentDir {
			break // Reached root directory
		}
		currentDir = parent
	}

	return "", fmt.Errorf("avatar.png not found in docs/ directory")
}

// convertImageToASCII converts an image to colored ASCII art
func convertImageToASCII(imagePath string) (string, error) {
	// Validate the path to prevent directory traversal
	cleanPath := filepath.Clean(imagePath)
	if !filepath.IsAbs(cleanPath) {
		// For relative paths, ensure they don't contain ".."
		if filepath.HasPrefix(cleanPath, "..") {
			return "", fmt.Errorf("invalid path: %s", imagePath)
		}
	}

	// Open the image file
	file, err := os.Open(cleanPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// Decode the image
	img, _, err := image.Decode(file)
	if err != nil {
		return "", err
	}

	// Get image bounds
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Calculate scaling factor to fit in terminal (max 80 chars wide)
	maxWidth := 80
	scale := float64(maxWidth) / float64(width)
	if scale > 1.0 {
		scale = 1.0
	}

	newWidth := int(float64(width) * scale)
	newHeight := int(float64(height) * scale)

	// ASCII characters from darkest to lightest
	asciiChars := []string{" ", ".", ":", ";", "o", "x", "%", "#", "@"}

	var result string

	for y := 0; y < newHeight; y++ {
		for x := 0; x < newWidth; x++ {
			// Map scaled coordinates back to original image
			srcX := int(float64(x) / scale)
			srcY := int(float64(y) / scale)

			// Ensure we don't go out of bounds
			if srcX >= width {
				srcX = width - 1
			}
			if srcY >= height {
				srcY = height - 1
			}

			// Get the pixel color
			r, g, b, a := img.At(srcX, srcY).RGBA()

			// Skip transparent pixels
			if a == 0 {
				result += " "
				continue
			}

			// Convert to 0-255 range, ensuring no overflow
			// RGBA() returns values in range [0, 0xFFFF], after >> 8 values are in range [0, 0xFF]
			r8 := uint8(r >> 8) // #nosec G115
			g8 := uint8(g >> 8) // #nosec G115
			b8 := uint8(b >> 8) // #nosec G115

			// Calculate brightness (luminance)
			brightness := float64(r8)*0.299 + float64(g8)*0.587 + float64(b8)*0.114

			// Map brightness to ASCII character
			charIndex := int(brightness * float64(len(asciiChars)-1) / 255.0)
			if charIndex >= len(asciiChars) {
				charIndex = len(asciiChars) - 1
			}

			// Add ANSI color code for the character
			ansiColor := fmt.Sprintf("\033[38;2;%d;%d;%dm", r8, g8, b8)
			result += ansiColor + asciiChars[charIndex]
		}
		result += "\033[0m\n" // Reset color and newline
	}

	return result, nil
}
