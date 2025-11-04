package util

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

// PromptChoice represents a user's choice in an interactive prompt
type PromptChoice string

const (
	PromptChoiceYes     PromptChoice = "yes"
	PromptChoiceNo      PromptChoice = "no"
	PromptChoiceAll     PromptChoice = "all"
	PromptChoiceQuit    PromptChoice = "quit"
	PromptChoiceDiff    PromptChoice = "diff"
	PromptChoiceEdit    PromptChoice = "edit"
	PromptChoiceHelp    PromptChoice = "help"
	PromptChoiceUnknown PromptChoice = "unknown"
)

// UpdateChoice represents a user's choice for template update workflow
type UpdateChoice string

const (
	UpdateChoiceApplyAll    UpdateChoice = "apply_all"
	UpdateChoiceReviewFiles UpdateChoice = "review_files"
	UpdateChoiceQuit        UpdateChoice = "quit"
)

// PromptUser prompts the user with a question and returns their choice
func PromptUser(question string) (PromptChoice, error) {
	fmt.Fprintf(os.Stderr, "%s [y]es / [n]o / [a]ll / [q]uit: ", question)

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return PromptChoiceUnknown, fmt.Errorf("failed to read user input: %w", err)
	}

	response = strings.TrimSpace(strings.ToLower(response))

	switch response {
	case "y", "yes":
		return PromptChoiceYes, nil
	case "n", "no":
		return PromptChoiceNo, nil
	case "a", "all":
		return PromptChoiceAll, nil
	case "q", "quit":
		return PromptChoiceQuit, nil
	default:
		return PromptChoiceUnknown, nil
	}
}

// PromptYesNo prompts the user with a yes/no question
func PromptYesNo(question string) (bool, error) {
	fmt.Fprintf(os.Stderr, "%s [y]es / [n]o: ", question)

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("failed to read user input: %w", err)
	}

	response = strings.TrimSpace(strings.ToLower(response))

	switch response {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, nil
	}
}

// PromptUserEnhanced prompts the user with enhanced options for file updates
// Supports: yes, no, all, quit, diff (re-display), edit (open in editor), help
func PromptUserEnhanced(question string, content string, filePath string) (PromptChoice, error) {
	const maxDiffShows = 5
	diffShownCount := 0

	for {
		fmt.Fprintf(os.Stderr, "%s [y]es / [n]o / [a]ll / [q]uit / [d]etails / [e]dit / [h]elp: ", question)

		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return PromptChoiceUnknown, fmt.Errorf("failed to read user input: %w", err)
		}

		response = strings.TrimSpace(strings.ToLower(response))

		switch response {
		case "y", "yes":
			return PromptChoiceYes, nil
		case "n", "no":
			return PromptChoiceNo, nil
		case "a", "all":
			return PromptChoiceAll, nil
		case "q", "quit":
			return PromptChoiceQuit, nil
		case "d", "diff":
			if diffShownCount >= maxDiffShows {
				fmt.Fprintf(os.Stderr, "Diff has been shown maximum times (%d)\n", maxDiffShows)
				continue
			}
			if err := DisplayContent(content); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to display diff: %v\n", err)
			}
			diffShownCount++
			continue
		case "e", "edit":
			if err := OpenInEditor(filePath); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to open editor: %v\n", err)
				continue
			}
			return PromptChoiceEdit, nil
		case "h", "help", "?":
			DisplayPromptHelp()
			continue
		default:
			fmt.Fprintln(os.Stderr, "Invalid choice. Press 'h' for help.")
			continue
		}
	}
}

// DisplayPromptHelp displays help text for interactive prompts
func DisplayPromptHelp() {
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Update options:")
	fmt.Fprintln(os.Stderr, "  y - Apply this change")
	fmt.Fprintln(os.Stderr, "  n - Skip this change")
	fmt.Fprintln(os.Stderr, "  a - Apply this change and all remaining changes automatically")
	fmt.Fprintln(os.Stderr, "  q - Quit update process (no more changes will be applied)")
	fmt.Fprintln(os.Stderr, "  d - Show full details (complete file or diff, up to 5 times per file)")
	fmt.Fprintln(os.Stderr, "  e - Open file in editor for manual merging")
	fmt.Fprintln(os.Stderr, "  h - Show this help")
	fmt.Fprintln(os.Stderr, "")
}

// OpenInEditor opens a file in the user's preferred editor
func OpenInEditor(filePath string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		// Fallback to common editors
		for _, e := range []string{"vim", "vi", "nano", "emacs"} {
			if _, err := exec.LookPath(e); err == nil {
				editor = e
				break
			}
		}
	}
	if editor == "" {
		return fmt.Errorf("no editor found. Set $EDITOR or $VISUAL environment variable")
	}

	// #nosec G204 - editor command comes from user's environment variable ($EDITOR/$VISUAL) which is intentional
	cmd := exec.Command(editor, filePath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// DisplayContent displays content with pagination if it exceeds terminal height
func DisplayContent(content string) error {
	lines := strings.Split(content, "\n")
	termHeight := getTerminalHeight()

	// If content fits in terminal, just print it
	if termHeight == 0 || len(lines) <= termHeight-5 { // -5 for prompt space
		fmt.Fprintln(os.Stderr, content)
		return nil
	}

	// Use pager for long content
	pager := os.Getenv("PAGER")
	if pager == "" {
		pager = "less"
	}

	// Check if pager exists
	if _, err := exec.LookPath(pager); err != nil {
		// Fallback: just print without paging
		fmt.Fprintln(os.Stderr, content)
		return nil
	}

	// #nosec G204 - pager command comes from user's environment variable ($PAGER) which is intentional
	cmd := exec.Command(pager, "-R") // -R for color support
	cmd.Stdin = strings.NewReader(content)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// getTerminalHeight returns the terminal height in lines, or 0 if unknown
func getTerminalHeight() int {
	// Try to get terminal size from stderr (where prompts go)
	if fd := int(os.Stderr.Fd()); term.IsTerminal(fd) {
		_, height, err := term.GetSize(fd)
		if err == nil {
			return height
		}
	}
	return 0
}

// ClearScreen clears the terminal screen
func ClearScreen() {
	// ANSI escape code to clear screen and move cursor to top-left
	fmt.Fprint(os.Stderr, "\033[2J\033[H")
}

// PromptUserForUpdate prompts the user after displaying update summary
// Options: y=apply all, n=review files one-by-one, q=quit
func PromptUserForUpdate(question string) (UpdateChoice, error) {
	fmt.Fprintf(os.Stderr, "%s [y=apply all / n=review files / q=quit]: ", question)

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return UpdateChoiceQuit, fmt.Errorf("failed to read user input: %w", err)
	}

	response = strings.TrimSpace(strings.ToLower(response))

	switch response {
	case "y", "yes":
		return UpdateChoiceApplyAll, nil
	case "n", "no":
		return UpdateChoiceReviewFiles, nil
	case "q", "quit":
		return UpdateChoiceQuit, nil
	default:
		// Default to review files (safer option)
		fmt.Fprintln(os.Stderr, "Invalid choice, defaulting to file-by-file review")
		return UpdateChoiceReviewFiles, nil
	}
}

// ColorCode represents ANSI color codes for terminal output
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorCyan   = "\033[36m"
)

// isTerminal returns true if stderr is a terminal (supports colors)
func isTerminal() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// ColorizeDiff adds color codes to unified diff output
// Lines starting with '+' are green, '-' are red, '@@' are cyan
func ColorizeDiff(diff string) string {
	if !isTerminal() {
		return diff
	}

	lines := strings.Split(diff, "\n")
	for i, line := range lines {
		if len(line) == 0 {
			continue
		}

		switch line[0] {
		case '+':
			if !strings.HasPrefix(line, "+++") {
				lines[i] = ColorGreen + line + ColorReset
			} else {
				lines[i] = ColorCyan + line + ColorReset
			}
		case '-':
			if !strings.HasPrefix(line, "---") {
				lines[i] = ColorRed + line + ColorReset
			} else {
				lines[i] = ColorCyan + line + ColorReset
			}
		case '@':
			if strings.HasPrefix(line, "@@") {
				lines[i] = ColorCyan + line + ColorReset
			}
		}
	}

	return strings.Join(lines, "\n")
}
