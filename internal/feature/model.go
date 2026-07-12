package feature

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// FrontMatter represents the YAML header of a feature spec.
type FrontMatter struct {
	ID                  string   `yaml:"id" json:"id"`
	Title               string   `yaml:"title" json:"title"`
	Status              string   `yaml:"status" json:"status"`
	Owner               string   `yaml:"owner" json:"owner"`
	ImplementationOwner string   `yaml:"implementation_owner" json:"implementation_owner"`
	Priority            string   `yaml:"priority" json:"priority"`
	Complexity          string   `yaml:"complexity" json:"complexity"`
	Created             string   `yaml:"created" json:"created"`
	Updated             string   `yaml:"updated" json:"updated"`
	StatusChanged       string   `yaml:"status_changed" json:"status_changed"`
	Labels              []string `yaml:"labels" json:"labels"`
	Dependencies        []string `yaml:"dependencies" json:"dependencies"`
	Epic                string   `yaml:"epic,omitempty" json:"epic,omitempty"`
	RiskNotes           string   `yaml:"risk_notes" json:"risk_notes"`
}

// Feature wraps a feature spec file with parsed components.
type Feature struct {
	Path        string
	FrontMatter FrontMatter
	Body        string
	source      *sourceSnapshot
}

// Parse converts raw markdown into a Feature structure.
func Parse(path string, data []byte) (*Feature, error) {
	frontmatter, body, ok := splitFrontmatter(data)
	if !ok {
		return nil, errors.New("invalid feature spec: missing frontmatter")
	}

	var fm FrontMatter
	decoder := yaml.NewDecoder(bytes.NewReader(frontmatter))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fm); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	return &Feature{
		Path:        path,
		FrontMatter: fm,
		Body:        string(body),
	}, nil
}

func splitFrontmatter(data []byte) (frontmatter, body []byte, ok bool) {
	firstLineEnd := bytes.IndexByte(data, '\n')
	if firstLineEnd < 0 {
		return nil, nil, false
	}
	firstLine := data[:firstLineEnd]
	if len(firstLine) > 0 && firstLine[len(firstLine)-1] == '\r' {
		firstLine = firstLine[:len(firstLine)-1]
	}
	if !bytes.Equal(firstLine, []byte("---")) {
		return nil, nil, false
	}

	headerStart := firstLineEnd + 1
	for lineStart := headerStart; lineStart <= len(data); {
		relativeEnd := bytes.IndexByte(data[lineStart:], '\n')
		lineEnd := len(data)
		nextLine := len(data)
		if relativeEnd >= 0 {
			lineEnd = lineStart + relativeEnd
			nextLine = lineEnd + 1
		}
		contentEnd := lineEnd
		if contentEnd > lineStart && data[contentEnd-1] == '\r' {
			contentEnd--
		}
		if bytes.Equal(data[lineStart:contentEnd], []byte("---")) {
			return data[headerStart:lineStart], data[nextLine:], true
		}
		if relativeEnd < 0 {
			break
		}
		lineStart = nextLine
	}
	return nil, nil, false
}

// Encode serialises the feature back into markdown format.
func (f *Feature) Encode() ([]byte, error) {
	fmBytes, err := yaml.Marshal(f.FrontMatter)
	if err != nil {
		return nil, fmt.Errorf("failed to encode frontmatter: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(fmBytes)
	if !bytes.HasSuffix(fmBytes, []byte("\n")) {
		buf.WriteByte('\n')
	}
	buf.WriteString("---\n")
	buf.WriteString(f.Body)
	return buf.Bytes(), nil
}

// UpdateTimestamp sets the updated date to today.
func (f *Feature) UpdateTimestamp() {
	f.FrontMatter.Updated = time.Now().Format("2006-01-02")
}

// StatusDirectory returns the current status directory based on status.
func (f *Feature) StatusDirectory(root string) string {
	dir := DirectoryForStatus(f.FrontMatter.Status)
	if dir == "" {
		return ""
	}
	return filepath.Join(root, dir)
}

// SetSection replaces a body section identified by an H2 heading (##).
func (f *Feature) SetSection(section, content string) error {
	normalized := strings.TrimSpace(section)
	if normalized == "" {
		return errors.New("section name is required")
	}
	updated, err := replaceSection(f.Body, normalized, content)
	if err != nil {
		return err
	}
	f.Body = updated
	return nil
}

// AddMissingSections ensures that all provided sections exist in the body.
func (f *Feature) AddMissingSections(order []string, defaults map[string]string) error {
	updated, changed, err := addMissingSections(f.Body, order, defaults)
	if err != nil {
		return err
	}
	if changed {
		f.Body = updated
	}
	return nil
}

// SetField updates a frontmatter property by key.
func (f *Feature) SetField(key, value string) error {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "id", "status", "owner", "created", "updated", "implementation_owner", "status_changed":
		return fmt.Errorf("field %s is managed by the lifecycle and cannot be updated directly", key)
	case "title":
		f.FrontMatter.Title = value
	case "priority":
		f.FrontMatter.Priority = value
	case "complexity":
		f.FrontMatter.Complexity = value
	case "epic":
		f.FrontMatter.Epic = value
	case "risk_notes":
		f.FrontMatter.RiskNotes = value
	case "labels":
		f.FrontMatter.Labels = splitList(value)
	case "dependencies":
		f.FrontMatter.Dependencies = splitList(value)
	default:
		return fmt.Errorf("unknown field %s", key)
	}
	return nil
}

// LabelsAsYAML converts labels to YAML sequence notation used in logs.
func (f *Feature) LabelsAsYAML() string {
	if len(f.FrontMatter.Labels) == 0 {
		return "[]"
	}
	quoted := make([]string, len(f.FrontMatter.Labels))
	for i, label := range f.FrontMatter.Labels {
		quoted[i] = fmt.Sprintf("\"%s\"", label)
	}
	return fmt.Sprintf("[%s]", strings.Join(quoted, ", "))
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
