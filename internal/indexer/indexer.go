package indexer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"path/filepath"
	"sort"
	"strings"

	"github.com/virtualboard/vb-cli/internal/feature"
)

// GenerationMarker identifies the canonical deterministic index format. It is
// deliberately independent of wall-clock time so unchanged feature state
// renders byte-for-byte identical output.
const GenerationMarker = "feature-state-v1"

// Entry represents a single feature within the index.
type Entry struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Status        string   `json:"status"`
	Owner         string   `json:"owner"`
	Priority      string   `json:"priority"`
	Complexity    string   `json:"complexity"`
	Labels        []string `json:"labels"`
	Updated       string   `json:"updated"`
	StatusChanged string   `json:"status_changed"`
	Path          string   `json:"path"`
}

// Data is the structured representation of the index.
type Data struct {
	Generated string         `json:"generated"`
	Features  []Entry        `json:"features"`
	Summary   map[string]int `json:"summary"`
}

// Generator produces indexes in multiple formats.
type Generator struct {
	mgr *feature.Manager
}

// NewGenerator constructs a new generator.
func NewGenerator(mgr *feature.Manager) *Generator {
	return &Generator{mgr: mgr}
}

// Build constructs index data with metadata.
func (g *Generator) Build() (*Data, error) {
	features, err := g.mgr.List()
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(features))
	summary := map[string]int{}
	featuresDir := g.mgr.FeaturesDir()

	for _, feat := range features {
		rel, err := filepath.Rel(featuresDir, feat.Path)
		if err != nil {
			rel = filepath.Base(feat.Path)
		}
		labels := append([]string{}, feat.FrontMatter.Labels...)
		entry := Entry{
			ID:            feat.FrontMatter.ID,
			Title:         feat.FrontMatter.Title,
			Status:        feat.FrontMatter.Status,
			Owner:         fallback(feat.FrontMatter.Owner, "unassigned"),
			Priority:      feat.FrontMatter.Priority,
			Complexity:    feat.FrontMatter.Complexity,
			Labels:        labels,
			Updated:       feat.FrontMatter.Updated,
			StatusChanged: feat.FrontMatter.StatusChanged,
			Path:          filepath.ToSlash(rel),
		}
		entries = append(entries, entry)
		summary[strings.ToLower(entry.Status)]++
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ID != entries[j].ID {
			return entries[i].ID < entries[j].ID
		}
		if entries[i].Path != entries[j].Path {
			return entries[i].Path < entries[j].Path
		}
		if entries[i].Status != entries[j].Status {
			return entries[i].Status < entries[j].Status
		}
		return entries[i].Title < entries[j].Title
	})

	return &Data{
		Generated: GenerationMarker,
		Features:  entries,
		Summary:   summary,
	}, nil
}

// Markdown renders the index as a Markdown table.
func (g *Generator) Markdown(data *Data) (string, error) {
	var b strings.Builder
	b.WriteString("# Features Index\n\n")
	b.WriteString("> Auto-generated from feature state - Do not edit manually\n\n")
	b.WriteString("| ID | Title | Status | Owner | P | C | Labels | Updated | Status Changed | File |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")

	for _, entry := range data.Features {
		labels := escapeMarkdownCell(strings.Join(entry.Labels, ", "))
		relPath := filepath.ToSlash(entry.Path)
		link := fmt.Sprintf("[%s](%s)", escapeMarkdownCell(relPath), relPath)
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			escapeMarkdownCell(entry.ID),
			escapeMarkdownCell(entry.Title),
			escapeMarkdownCell(entry.Status),
			escapeMarkdownCell(entry.Owner),
			escapeMarkdownCell(entry.Priority),
			escapeMarkdownCell(entry.Complexity),
			labels,
			escapeMarkdownCell(entry.Updated),
			escapeMarkdownCell(entry.StatusChanged),
			link,
		))
	}

	b.WriteString("\n## Summary\n\n")
	keys := make([]string, 0, len(data.Summary))
	for status := range data.Summary {
		keys = append(keys, status)
	}
	sort.Strings(keys)
	for _, status := range keys {
		b.WriteString(fmt.Sprintf("- **%s**: %d\n", escapeMarkdownCell(status), data.Summary[status]))
	}
	b.WriteString(fmt.Sprintf("\n**Total**: %d features\n", len(data.Features)))

	return b.String(), nil
}

func escapeMarkdownCell(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
	return html.EscapeString(value)
}

// JSON renders the index as JSON.
func (g *Generator) JSON(data *Data) (string, error) {
	buf, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", err
	}
	return string(buf) + "\n", nil
}

// HTML renders the index as a simple HTML table.
func (g *Generator) HTML(data *Data) (string, error) {
	tpl := `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<title>VirtualBoard Index</title>
<style>
body { font-family: system-ui, sans-serif; margin: 2rem; }
table { border-collapse: collapse; width: 100%; }
th, td { border: 1px solid #ccc; padding: 0.5rem; text-align: left; }
th { background: #f5f5f5; }
caption { caption-side: top; font-weight: bold; margin-bottom: 1rem; }
</style>
</head>
<body>
<table>
<caption>Features Index</caption>
<thead><tr><th>ID</th><th>Title</th><th>Status</th><th>Owner</th><th>Priority</th><th>Complexity</th><th>Labels</th><th>Updated</th><th>Status Changed</th><th>File</th></tr></thead>
<tbody>
{{ range .Features }}
<tr>
<td>{{ .ID }}</td>
<td>{{ .Title }}</td>
<td>{{ .Status }}</td>
<td>{{ .Owner }}</td>
<td>{{ .Priority }}</td>
<td>{{ .Complexity }}</td>
<td>{{ join .Labels ", " }}</td>
<td>{{ .Updated }}</td>
<td>{{ .StatusChanged }}</td>
<td><a href="{{ .Path }}">{{ .Path }}</a></td>
</tr>
{{ end }}
</tbody>
</table>
<section>
<h2>Summary</h2>
<ul>
{{ range $status, $count := .Summary }}
<li><strong>{{ $status }}</strong>: {{ $count }}</li>
{{ end }}
</ul>
<p><strong>Total:</strong> {{ len .Features }} features</p>
</section>
</body>
</html>`

	funcMap := template.FuncMap{
		"join": strings.Join,
	}

	t, err := template.New("index").Funcs(funcMap).Parse(tpl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}
