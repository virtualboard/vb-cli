package util

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// PromptChoice represents a user's choice in an interactive prompt
type PromptChoice string

const (
	PromptChoiceYes     PromptChoice = "yes"
	PromptChoiceNo      PromptChoice = "no"
	PromptChoiceAll     PromptChoice = "all"
	PromptChoiceQuit    PromptChoice = "quit"
	PromptChoiceUnknown PromptChoice = "unknown"
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
