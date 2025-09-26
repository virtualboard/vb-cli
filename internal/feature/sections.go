package feature

import "strings"

type documentSections struct {
	Intro string
	Order []string
	Data  map[string]string
}

func parseSections(body string) documentSections {
	sections := documentSections{
		Data: make(map[string]string),
	}

	lines := strings.Split(body, "\n")
	var current string
	buffer := make([]string, 0, len(lines))

	flush := func() {
		chunk := strings.Join(buffer, "\n")
		if current == "" {
			sections.Intro = strings.TrimRight(chunk, "\n")
		} else {
			sections.Data[current] = strings.TrimSpace(chunk)
		}
		buffer = buffer[:0]
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flush()
			current = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			found := false
			for _, existing := range sections.Order {
				if existing == current {
					found = true
					break
				}
			}
			if !found {
				sections.Order = append(sections.Order, current)
			}
			continue
		}
		buffer = append(buffer, line)
	}

	flush()

	return sections
}

func rebuildBody(sections documentSections) string {
	var sb strings.Builder
	intro := strings.TrimRight(sections.Intro, "\n")
	if intro != "" {
		sb.WriteString(intro)
		sb.WriteString("\n\n")
	}
	for _, name := range sections.Order {
		sb.WriteString("## ")
		sb.WriteString(name)
		sb.WriteString("\n")
		content := strings.TrimSpace(sections.Data[name])
		if content != "" {
			sb.WriteString(content)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	result := strings.TrimRight(sb.String(), "\n")
	return result + "\n"
}

// ExtractSections exposes the section order and defaults for external packages.
func ExtractSections(body string) (order []string, defaults map[string]string) {
	parsed := parseSections(body)
	order = append(order, parsed.Order...)
	defaults = make(map[string]string, len(parsed.Data))
	for k, v := range parsed.Data {
		defaults[k] = v
	}
	return order, defaults
}
