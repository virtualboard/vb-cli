package spec

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var approvedBodySections = map[string][]string{
	"tech-stack": {
		"Purpose", "Architecture Overview", "Languages, Frameworks & Tooling",
		"Security & Compliance Considerations", "Operational Considerations",
	},
	"local-development": {
		"Purpose", "Prerequisites", "Quick Start Checklist", "Running the Stack", "Testing Workflows",
	},
	"hosting-and-infrastructure": {
		"Purpose", "Environment Overview", "Reference Architecture",
		"Resilience & Disaster Recovery", "Operational Runbooks",
	},
	"ci-cd-pipeline": {
		"Purpose", "Pipeline Overview", "Triggers & Branching Strategy", "Stage Details", "Failure Handling & Recovery",
	},
	"database-schema": {
		"Purpose", "Schema Summary", "Entity Inventory", "Relationships & Data Flows", "Migrations & Change Management",
	},
	"caching-and-performance": {
		"Purpose", "Performance Targets", "Caching Layers", "Invalidation & Consistency", "Failure & Fallback Behavior",
	},
	"security-and-compliance": {
		"Purpose", "Threat Modeling", "Identity, Authentication & Authorization",
		"Data Protection Controls", "Vulnerability & Incident Response",
	},
	"observability-and-incident-response": {
		"Purpose", "Objectives & SLIs", "Telemetry Coverage", "Alerting Strategy", "Incident Response Lifecycle",
	},
}

var approvedPlaceholderPrefix = regexp.MustCompile(`(?i)^(?:todo|tbd|fixme|placeholder|coming soon|details?\.?|describe\b|provide\b|not yet defined\b)`)

type bodySection struct {
	name    string
	content strings.Builder
}

func validateApprovedBody(specType, body string) []string {
	required, known := approvedBodySections[specType]
	if !known {
		return nil
	}
	sections := parseBodySections(body)
	byName := make(map[string][]*bodySection, len(sections))
	positions := make(map[string]int, len(sections))
	for index := range sections {
		section := &sections[index]
		key := strings.ToLower(strings.TrimSpace(section.name))
		byName[key] = append(byName[key], section)
		if _, exists := positions[key]; !exists {
			positions[key] = index
		}
	}

	validationErrors := []string{}
	previousPosition := -1
	totalWords := 0
	for _, heading := range required {
		key := strings.ToLower(heading)
		matches := byName[key]
		if len(matches) == 0 {
			validationErrors = append(validationErrors, fmt.Sprintf("approved %s spec requires section %q", specType, heading))
			continue
		}
		if len(matches) > 1 {
			validationErrors = append(validationErrors, fmt.Sprintf("approved %s spec section %q appears %d times", specType, heading, len(matches)))
			continue
		}
		position := positions[key]
		if position < previousPosition {
			validationErrors = append(validationErrors, fmt.Sprintf("approved %s spec sections are out of canonical order at %q", specType, heading))
		}
		previousPosition = position
		content := strings.TrimSpace(matches[0].content.String())
		words := meaningfulWords(content)
		totalWords += len(words)
		plain := strings.Join(words, " ")
		if len(words) < 3 || approvedPlaceholderPrefix.MatchString(plain) {
			validationErrors = append(validationErrors, fmt.Sprintf("approved %s spec section %q must contain a meaningful decision or instruction", specType, heading))
		}
	}
	if len(validationErrors) == 0 && totalWords < 20 {
		validationErrors = append(validationErrors, fmt.Sprintf("approved %s spec body is too sparse to establish an operational decision", specType))
	}
	return validationErrors
}

func parseBodySections(body string) []bodySection {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	sections := []bodySection{}
	current := -1
	fenceMarker := byte(0)
	fenceLength := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if marker, length, ok := markdownFence(trimmed); ok {
			if fenceMarker == 0 {
				fenceMarker, fenceLength = marker, length
			} else if marker == fenceMarker && length >= fenceLength {
				fenceMarker, fenceLength = 0, 0
			}
			if current >= 0 {
				sections[current].content.WriteString(line)
				sections[current].content.WriteByte('\n')
			}
			continue
		}
		if fenceMarker == 0 && strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, "### ") {
			heading := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")), "#"))
			sections = append(sections, bodySection{name: heading})
			current = len(sections) - 1
			continue
		}
		if current >= 0 {
			sections[current].content.WriteString(line)
			sections[current].content.WriteByte('\n')
		}
	}
	return sections
}

func markdownFence(line string) (byte, int, bool) {
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return 0, 0, false
	}
	marker := line[0]
	length := 0
	for length < len(line) && line[length] == marker {
		length++
	}
	return marker, length, length >= 3
}

func meaningfulWords(content string) []string {
	words := []string{}
	for _, field := range strings.FieldsFunc(content, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if field != "" {
			words = append(words, field)
		}
	}
	return words
}
