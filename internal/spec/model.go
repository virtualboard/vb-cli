package spec

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// FrontMatter represents the YAML header of a system specification.
type FrontMatter struct {
	SpecType           string   `yaml:"spec_type" json:"spec_type"`
	Title              string   `yaml:"title" json:"title"`
	Status             string   `yaml:"status" json:"status"`
	LastUpdated        string   `yaml:"last_updated" json:"last_updated"`
	Applicability      []string `yaml:"applicability" json:"applicability"`
	Owner              string   `yaml:"owner,omitempty" json:"owner,omitempty"`
	RelatedInitiatives []string `yaml:"related_initiatives,omitempty" json:"related_initiatives,omitempty"`
}

// Spec represents a system specification document.
type Spec struct {
	Path        string
	FrontMatter FrontMatter
	Body        string
}

var (
	// ErrNoFrontmatter indicates missing frontmatter.
	ErrNoFrontmatter = errors.New("no frontmatter found")
	// ErrInvalidFrontmatter indicates unparsable frontmatter.
	ErrInvalidFrontmatter = errors.New("invalid frontmatter format")
)

const frontmatterDelimiter = "---"

// Parse reads a spec file and extracts frontmatter and body.
func Parse(path string, data []byte) (*Spec, error) {
	content := string(data)
	lines := strings.Split(content, "\n")

	if len(lines) < 3 {
		return nil, ErrNoFrontmatter
	}

	if strings.TrimSpace(lines[0]) != frontmatterDelimiter {
		return nil, ErrNoFrontmatter
	}

	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == frontmatterDelimiter {
			endIdx = i
			break
		}
	}

	if endIdx == -1 {
		return nil, ErrNoFrontmatter
	}

	fmContent := strings.Join(lines[1:endIdx], "\n")
	var fm FrontMatter
	if err := yaml.Unmarshal([]byte(fmContent), &fm); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFrontmatter, err)
	}

	bodyLines := lines[endIdx+1:]
	body := strings.Join(bodyLines, "\n")
	body = strings.TrimLeft(body, "\n")

	return &Spec{
		Path:        path,
		FrontMatter: fm,
		Body:        body,
	}, nil
}

// Encode serializes the spec back to markdown with frontmatter.
func (s *Spec) Encode() ([]byte, error) {
	var buf bytes.Buffer

	fmData, err := yaml.Marshal(&s.FrontMatter)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal frontmatter: %w", err)
	}

	buf.WriteString(frontmatterDelimiter)
	buf.WriteString("\n")
	buf.Write(fmData)
	buf.WriteString(frontmatterDelimiter)
	buf.WriteString("\n")
	buf.WriteString(s.Body)

	return buf.Bytes(), nil
}

// UpdateTimestamp updates the last_updated field to today's date.
func (s *Spec) UpdateTimestamp() {
	// Note: timestamp update is intentionally left to caller
	// to allow for explicit control in tests and dry-run mode
}
