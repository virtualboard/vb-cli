package indexer

import (
	"bufio"
	"strings"
)

// ParseMarkdown parses an existing INDEX.md file and returns the Data structure.
// This allows comparison with newly generated data to detect changes.
func ParseMarkdown(content string) (*Data, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))

	var entries []Entry
	inTable := false
	headerSeen := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines
		if line == "" {
			continue
		}

		// Detect table start - look for the header row
		if strings.HasPrefix(line, "| ID |") {
			inTable = true
			headerSeen = false
			continue
		}

		// Skip separator line
		if strings.HasPrefix(line, "|---") {
			headerSeen = true
			continue
		}

		// Parse table rows
		if inTable && headerSeen && strings.HasPrefix(line, "|") {
			// Check if we've reached the end of the table (Summary section)
			if strings.HasPrefix(line, "## ") {
				inTable = false
				continue
			}

			entry, err := parseTableRow(line)
			if err != nil {
				// If we can't parse a row, we might have hit the end of the table
				inTable = false
				continue
			}
			// Skip empty entries (malformed rows)
			if entry.ID != "" {
				entries = append(entries, entry)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Build summary from entries
	summary := make(map[string]int)
	for _, entry := range entries {
		summary[strings.ToLower(entry.Status)]++
	}

	return &Data{
		Features: entries,
		Summary:  summary,
	}, nil
}

// parseTableRow parses a single markdown table row into an Entry.
// Expected format: | ID | Title | Status | Owner | P | C | Labels | Updated | File |
func parseTableRow(line string) (Entry, error) {
	// Remove leading/trailing pipes and split by pipe
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")

	// We expect 9 columns
	if len(parts) < 9 {
		return Entry{}, nil
	}

	// Trim whitespace from each part
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}

	// Parse labels (comma-separated)
	var labels []string
	if parts[6] != "" {
		labelParts := strings.Split(parts[6], ",")
		for _, label := range labelParts {
			trimmed := strings.TrimSpace(label)
			if trimmed != "" {
				labels = append(labels, trimmed)
			}
		}
	}

	// Extract path from markdown link format: [path](url)
	path := parts[8]
	if strings.Contains(path, "](") {
		// Extract the path from [path](url) format
		start := strings.Index(path, "[")
		end := strings.Index(path, "]")
		if start >= 0 && end > start {
			path = path[start+1 : end]
		}
	}

	return Entry{
		ID:         parts[0],
		Title:      parts[1],
		Status:     parts[2],
		Owner:      parts[3],
		Priority:   parts[4],
		Complexity: parts[5],
		Labels:     labels,
		Updated:    parts[7],
		Path:       path,
	}, nil
}
