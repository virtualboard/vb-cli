package feature

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var frontmatterPattern = regexp.MustCompile(`(?s)^---\n(.*?)\n---\n(.*)$`)

// FrontMatter represents the YAML header of a feature spec.
type FrontMatter struct {
	ID           string   `yaml:"id" json:"id"`
	Title        string   `yaml:"title" json:"title"`
	Status       string   `yaml:"status" json:"status"`
	Owner        string   `yaml:"owner" json:"owner"`
	Priority     string   `yaml:"priority" json:"priority"`
	Complexity   string   `yaml:"complexity" json:"complexity"`
	Created      string   `yaml:"created" json:"created"`
	Updated      string   `yaml:"updated" json:"updated"`
	Labels       []string `yaml:"labels" json:"labels"`
	Dependencies []string `yaml:"dependencies" json:"dependencies"`
	Epic         string   `yaml:"epic" json:"epic"`
	RiskNotes    string   `yaml:"risk_notes" json:"risk_notes"`
}

// Feature wraps a feature spec file with parsed components.
type Feature struct {
	Path        string
	FrontMatter FrontMatter
	Body        string
}

// Parse converts raw markdown into a Feature structure.
func Parse(path string, data []byte) (*Feature, error) {
	matches := frontmatterPattern.FindSubmatch(data)
	if len(matches) != 3 {
		return nil, errors.New("invalid feature spec: missing frontmatter")
	}

	var fm FrontMatter
	if err := yaml.Unmarshal(matches[1], &fm); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	return &Feature{
		Path:        path,
		FrontMatter: fm,
		Body:        string(bytes.TrimPrefix(matches[2], []byte("\n"))),
	}, nil
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
	body := strings.TrimLeft(f.Body, "\n")
	buf.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		buf.WriteByte('\n')
	}
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
	sections := parseSections(f.Body)
	normalized := strings.TrimSpace(section)
	if _, ok := sections.Data[normalized]; !ok {
		return fmt.Errorf("section %q not found", section)
	}
	sections.Data[normalized] = strings.TrimSpace(content)
	f.Body = rebuildBody(sections)
	return nil
}

// AddMissingSections ensures that all provided sections exist in the body.
func (f *Feature) AddMissingSections(order []string, defaults map[string]string) {
	sections := parseSections(f.Body)
	changed := false
	for _, name := range order {
		if _, ok := sections.Data[name]; !ok {
			sections.Order = append(sections.Order, name)
			if defaults != nil {
				sections.Data[name] = defaults[name]
			} else {
				sections.Data[name] = ""
			}
			changed = true
		}
	}
	if changed {
		f.Body = rebuildBody(sections)
	}
}

// SetField updates a frontmatter property by key.
func (f *Feature) SetField(key, value string) error {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "id":
		f.FrontMatter.ID = strings.TrimSpace(value)
	case "title":
		f.FrontMatter.Title = value
	case "status":
		f.FrontMatter.Status = strings.ToLower(strings.TrimSpace(value))
	case "owner":
		f.FrontMatter.Owner = value
	case "priority":
		f.FrontMatter.Priority = value
	case "complexity":
		f.FrontMatter.Complexity = value
	case "created":
		f.FrontMatter.Created = strings.TrimSpace(value)
	case "updated":
		f.FrontMatter.Updated = strings.TrimSpace(value)
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
