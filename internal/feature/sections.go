package feature

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const (
	untrustedContentOpen  = "<untrusted-content>"
	untrustedContentClose = "</untrusted-content>"
)

var canonicalSectionOrder = []string{
	"Summary",
	"Problem Statement",
	"Goals & Non-Goals",
	"User Stories",
	"Requirements",
	"Acceptance Criteria (Testable)",
	"UI/UX Notes",
	"Data & API",
	"Rollout & Migration",
	"Monitoring & Metrics",
	"Security & Compliance",
	"Implementation Notes",
	"Open Questions",
	"Links",
}

var (
	checklistItemPattern = regexp.MustCompile(`^[\t ]*(?:[-+*]|[0-9]{1,9}[.)])[\t ]+\[([ xX])\][\t ]+(.*)$`)
	markdownLinkPattern  = regexp.MustCompile(`\[[^]\r\n]+\]\(([^)\r\n]+)\)`)
	urlPattern           = regexp.MustCompile(`(?i)\bhttps?://[^\s<>]+`)
	featureRefPattern    = regexp.MustCompile(`\bFTR-[0-9]{4}\b`)
	workItemRefPattern   = regexp.MustCompile(`(?i)\b(?:PR|issue|ticket)[\t ]*(?:#|:)[\t ]*[A-Za-z0-9][A-Za-z0-9._/-]*\b`)
	hashRefPattern       = regexp.MustCompile(`(?:^|[\s(])#[0-9]+\b`)
	commitRefPattern     = regexp.MustCompile(`\b[0-9a-fA-F]{7,40}\b`)
	artifactPathPattern  = regexp.MustCompile(`(?:^|[\s(])(?:\.?\.?/)?[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+\.[A-Za-z0-9]{1,12}\b`)
	artifactFilePattern  = regexp.MustCompile(`(?:^|[\s(])[A-Za-z0-9_.-]+\.(?:md|html?|json|ya?ml|txt|log|go|py|js|ts|tsx|jsx|sh|pdf)\b`)
	placeholderPattern   = regexp.MustCompile(`<[A-Za-z][A-Za-z0-9 _.-]{0,80}>`)
	htmlCommentPattern   = regexp.MustCompile(`(?s)<!--.*?-->`)
)

type markdownSpan struct {
	Start int
	End   int
}

type sectionSpan struct {
	Name         string
	HeadingStart int
	HeadingEnd   int
	ContentStart int
	ContentEnd   int
}

type markdownDocument struct {
	Sections []sectionSpan
	Open     []markdownSpan
	Close    []markdownSpan
	Newline  string
}

type markdownLine struct {
	Start      int
	End        int
	ContentEnd int
	Text       string
	Newline    string
}

type fenceState struct {
	marker byte
	length int
}

// CanonicalSectionOrder returns a copy of the required feature-body H2 order.
func CanonicalSectionOrder() []string {
	return append([]string(nil), canonicalSectionOrder...)
}

func scanMarkdown(body string) markdownDocument {
	doc := markdownDocument{Newline: preferredLineEnding(body)}
	lines := markdownLines(body)
	var fence *fenceState
	for _, line := range lines {
		if fence != nil {
			if closesFence(line.Text, *fence) {
				fence = nil
			}
			continue
		}
		if opened, ok := opensFence(line.Text); ok {
			fence = &opened
			continue
		}

		switch markerLine(line.Text) {
		case untrustedContentOpen:
			doc.Open = append(doc.Open, markdownSpan{Start: line.Start, End: line.End})
		case untrustedContentClose:
			doc.Close = append(doc.Close, markdownSpan{Start: line.Start, End: line.End})
		}

		if name, ok := atxH2Name(line.Text); ok {
			doc.Sections = append(doc.Sections, sectionSpan{
				Name:         name,
				HeadingStart: line.Start,
				HeadingEnd:   line.End,
				ContentStart: line.End,
			})
		}
	}

	for i := range doc.Sections {
		end := len(body)
		if i+1 < len(doc.Sections) {
			end = doc.Sections[i+1].HeadingStart
		}
		for _, marker := range append(append([]markdownSpan(nil), doc.Open...), doc.Close...) {
			if marker.Start >= doc.Sections[i].ContentStart && marker.Start < end {
				end = marker.Start
			}
		}
		doc.Sections[i].ContentEnd = end
	}
	return doc
}

func markdownLines(body string) []markdownLine {
	if body == "" {
		return nil
	}
	lines := make([]markdownLine, 0, strings.Count(body, "\n")+1)
	for start := 0; start < len(body); {
		newlineIndex := strings.IndexByte(body[start:], '\n')
		if newlineIndex < 0 {
			contentEnd := len(body)
			if contentEnd > start && body[contentEnd-1] == '\r' {
				contentEnd--
			}
			lines = append(lines, markdownLine{
				Start: start, End: len(body), ContentEnd: contentEnd,
				Text: body[start:contentEnd],
			})
			break
		}
		newlineIndex += start
		contentEnd := newlineIndex
		newline := "\n"
		if contentEnd > start && body[contentEnd-1] == '\r' {
			contentEnd--
			newline = "\r\n"
		}
		lines = append(lines, markdownLine{
			Start: start, End: newlineIndex + 1, ContentEnd: contentEnd,
			Text: body[start:contentEnd], Newline: newline,
		})
		start = newlineIndex + 1
	}
	return lines
}

func preferredLineEnding(body string) string {
	if index := strings.IndexByte(body, '\n'); index >= 0 && index > 0 && body[index-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

func stripBlockIndent(line string) (string, bool) {
	spaces := 0
	for spaces < len(line) && line[spaces] == ' ' {
		spaces++
	}
	if spaces > 3 || (spaces < len(line) && line[spaces] == '\t') {
		return "", false
	}
	return line[spaces:], true
}

func markerLine(line string) string {
	rest, ok := stripBlockIndent(line)
	if !ok {
		return ""
	}
	rest = strings.TrimRight(rest, " \t")
	if rest == untrustedContentOpen || rest == untrustedContentClose {
		return rest
	}
	return ""
}

func opensFence(line string) (fenceState, bool) {
	rest, ok := stripBlockIndent(line)
	if !ok || len(rest) < 3 || (rest[0] != '`' && rest[0] != '~') {
		return fenceState{}, false
	}
	marker := rest[0]
	count := 0
	for count < len(rest) && rest[count] == marker {
		count++
	}
	if count < 3 {
		return fenceState{}, false
	}
	if marker == '`' && strings.ContainsRune(rest[count:], '`') {
		return fenceState{}, false
	}
	return fenceState{marker: marker, length: count}, true
}

func closesFence(line string, fence fenceState) bool {
	rest, ok := stripBlockIndent(line)
	if !ok || len(rest) < fence.length || rest[0] != fence.marker {
		return false
	}
	count := 0
	for count < len(rest) && rest[count] == fence.marker {
		count++
	}
	return count >= fence.length && strings.Trim(rest[count:], " \t") == ""
}

func atxH2Name(line string) (string, bool) {
	rest, ok := stripBlockIndent(line)
	if !ok || !strings.HasPrefix(rest, "##") || strings.HasPrefix(rest, "###") {
		return "", false
	}
	if len(rest) > 2 && rest[2] != ' ' && rest[2] != '\t' {
		return "", false
	}
	text := strings.Trim(rest[2:], " \t")
	if text == "" {
		return "", true
	}
	trimmedRight := strings.TrimRight(text, " \t")
	hashStart := len(trimmedRight)
	for hashStart > 0 && trimmedRight[hashStart-1] == '#' {
		hashStart--
	}
	if hashStart < len(trimmedRight) && hashStart > 0 && (trimmedRight[hashStart-1] == ' ' || trimmedRight[hashStart-1] == '\t') {
		text = strings.TrimRight(trimmedRight[:hashStart], " \t")
	}
	return text, true
}

func sectionsNamed(doc markdownDocument, name string) []sectionSpan {
	matches := make([]sectionSpan, 0, 1)
	for _, section := range doc.Sections {
		if section.Name == name {
			matches = append(matches, section)
		}
	}
	return matches
}

func canonicalRank(name string) (int, bool) {
	for index, canonical := range canonicalSectionOrder {
		if name == canonical {
			return index, true
		}
	}
	return 0, false
}

func repairableBodyErrors(doc markdownDocument) []string {
	errors := make([]string, 0)
	if len(doc.Open) != 1 {
		errors = append(errors, fmt.Sprintf("feature body must contain exactly one %s marker (found %d)", untrustedContentOpen, len(doc.Open)))
	}
	if len(doc.Close) != 1 {
		errors = append(errors, fmt.Sprintf("feature body must contain exactly one %s marker (found %d)", untrustedContentClose, len(doc.Close)))
	}
	boundaryOrdered := len(doc.Open) == 1 && len(doc.Close) == 1 && doc.Open[0].Start < doc.Close[0].Start
	if len(doc.Open) == 1 && len(doc.Close) == 1 && !boundaryOrdered {
		errors = append(errors, "the <untrusted-content> opening marker must precede its closing marker")
	}

	for _, canonical := range canonicalSectionOrder {
		matches := sectionsNamed(doc, canonical)
		if len(matches) > 1 {
			errors = append(errors, fmt.Sprintf("canonical section %q appears %d times", canonical, len(matches)))
			continue
		}
		if len(matches) == 0 {
			continue
		}
		if boundaryOrdered && (matches[0].HeadingStart <= doc.Open[0].Start || matches[0].HeadingStart >= doc.Close[0].Start) {
			errors = append(errors, fmt.Sprintf("canonical section %q must be inside the <untrusted-content> boundary", canonical))
		}
	}

	// The loop above follows canonical order, so detect actual out-of-order
	// headings separately from the set of present names.
	previousRank := -1
	orderError := false
	for _, section := range doc.Sections {
		rank, canonical := canonicalRank(section.Name)
		if !canonical {
			continue
		}
		if rank < previousRank {
			orderError = true
			break
		}
		previousRank = rank
	}
	if orderError {
		errors = appendUnique(errors, "canonical feature sections must appear in the required order")
	}
	return errors
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// ValidateBodyForStatus checks the canonical trust boundary, required H2
// structure, and the evidence gates for review and done features.
func ValidateBodyForStatus(body, status string) []string {
	doc := scanMarkdown(body)
	errors := repairableBodyErrors(doc)
	for _, canonical := range canonicalSectionOrder {
		if len(sectionsNamed(doc, canonical)) == 0 {
			errors = append(errors, fmt.Sprintf("canonical section %q is missing", canonical))
		}
	}

	status = strings.ToLower(strings.TrimSpace(status))
	if status != "review" && status != "done" {
		return errors
	}
	acceptance, ok := uniqueSectionContent(body, doc, "Acceptance Criteria (Testable)")
	if ok {
		items := checklistItems(acceptance)
		if len(items) == 0 {
			errors = append(errors, "Acceptance Criteria (Testable) must contain at least one non-placeholder checklist item before review")
		} else {
			placeholder := false
			incomplete := false
			for _, item := range items {
				if !meaningfulText(item.text) {
					placeholder = true
				}
				if !item.checked {
					incomplete = true
				}
			}
			if placeholder {
				errors = append(errors, "Acceptance Criteria (Testable) must not contain placeholder checklist items before review")
			}
			if incomplete {
				errors = append(errors, "all Acceptance Criteria (Testable) checklist items must be checked before review")
			}
		}
	}

	if status == "done" {
		implementationNotes, ok := uniqueSectionContent(body, doc, "Implementation Notes")
		if ok && !meaningfulImplementationEvidence(implementationNotes) {
			errors = append(errors, "Implementation Notes must contain meaningful implementation evidence before done")
		}
		links, ok := uniqueSectionContent(body, doc, "Links")
		if ok && !hasConcreteReference(links) {
			errors = append(errors, "Links must contain at least one concrete artifact or reference before done")
		}
	}
	return errors
}

func uniqueSectionContent(body string, doc markdownDocument, name string) (string, bool) {
	matches := sectionsNamed(doc, name)
	if len(matches) != 1 {
		return "", false
	}
	span := matches[0]
	if span.ContentStart < 0 || span.ContentEnd < span.ContentStart || span.ContentEnd > len(body) {
		return "", false
	}
	return strings.TrimSpace(body[span.ContentStart:span.ContentEnd]), true
}

type checklistItem struct {
	checked bool
	text    string
}

func checklistItems(content string) []checklistItem {
	items := make([]checklistItem, 0)
	for _, line := range visibleMarkdownLines(content) {
		matches := checklistItemPattern.FindStringSubmatch(line)
		if len(matches) != 3 {
			continue
		}
		items = append(items, checklistItem{
			checked: strings.EqualFold(matches[1], "x"),
			text:    strings.TrimSpace(matches[2]),
		})
	}
	return items
}

func visibleMarkdownLines(content string) []string {
	visible := make([]string, 0)
	var fence *fenceState
	for _, line := range markdownLines(content) {
		if fence != nil {
			if closesFence(line.Text, *fence) {
				fence = nil
			}
			continue
		}
		if opened, ok := opensFence(line.Text); ok {
			fence = &opened
			continue
		}
		visible = append(visible, line.Text)
	}
	return visible
}

func meaningfulText(value string) bool {
	value = strings.TrimSpace(htmlCommentPattern.ReplaceAllString(value, ""))
	value = strings.Trim(value, " \t\r\n-*_`~.")
	lower := strings.ToLower(value)
	if lower == "" || lower == "…" || lower == "todo" || lower == "tbd" || lower == "pending" || lower == "placeholder" || lower == "n/a" || lower == "none" {
		return false
	}
	if strings.HasPrefix(lower, "todo:") || strings.HasPrefix(lower, "tbd:") || strings.Contains(lower, "<placeholder>") {
		return false
	}
	if placeholderPattern.MatchString(value) {
		return false
	}
	alphanumeric := 0
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			alphanumeric++
		}
	}
	return alphanumeric >= 6
}

func meaningfulImplementationEvidence(content string) bool {
	content = htmlCommentPattern.ReplaceAllString(content, "")
	lines := make([]string, 0)
	for _, line := range visibleMarkdownLines(content) {
		line = strings.TrimSpace(strings.TrimLeft(line, "-+*"))
		if line != "" {
			lines = append(lines, line)
		}
	}
	value := strings.TrimSpace(strings.Join(lines, " "))
	if strings.EqualFold(strings.TrimRight(value, "."), "Libraries, patterns, risks, tech debt considerations") {
		return false
	}
	return meaningfulText(value)
}

func hasConcreteReference(content string) bool {
	content = htmlCommentPattern.ReplaceAllString(content, "")
	value := strings.TrimSpace(strings.Join(visibleMarkdownLines(content), "\n"))
	if !meaningfulText(value) || strings.EqualFold(strings.Trim(strings.TrimSpace(value), "-. "), "Related FTRs, tickets, PRs") {
		return false
	}
	if matches := markdownLinkPattern.FindAllStringSubmatch(value, -1); len(matches) > 0 {
		for _, match := range matches {
			target := strings.TrimSpace(match[1])
			if meaningfulText(target) && target != "#" {
				return true
			}
		}
	}
	// Inline-code paths are a common way to cite local test and report evidence.
	// Remove only the Markdown code-span delimiter for reference recognition;
	// the original visible value still drives placeholder and meaningful-text
	// checks above.
	referenceValue := strings.ReplaceAll(value, "`", "")
	return urlPattern.MatchString(referenceValue) ||
		featureRefPattern.MatchString(referenceValue) ||
		workItemRefPattern.MatchString(referenceValue) ||
		hashRefPattern.MatchString(referenceValue) ||
		commitRefPattern.MatchString(referenceValue) ||
		artifactPathPattern.MatchString(referenceValue) ||
		artifactFilePattern.MatchString(referenceValue)
}

func normalizeContentLineEndings(content, newline string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = strings.Trim(content, "\n")
	if newline != "\n" {
		content = strings.ReplaceAll(content, "\n", newline)
	}
	return content
}

func sectionReplacement(content, newline string) string {
	content = normalizeContentLineEndings(content, newline)
	if strings.TrimSpace(content) == "" {
		return newline
	}
	return content + newline + newline
}

func replaceSection(body, name, content string) (string, error) {
	if repaired, changed := repairLegacyLinksBoundary(body); changed {
		body = repaired
	}
	doc := scanMarkdown(body)
	if errors := repairableBodyErrors(doc); len(errors) > 0 {
		return "", fmt.Errorf("invalid feature body: %s", strings.Join(errors, "; "))
	}
	matches := sectionsNamed(doc, name)
	if len(matches) == 0 {
		return "", fmt.Errorf("section %q not found", name)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("section %q appears %d times", name, len(matches))
	}
	span := matches[0]
	newline := sectionLineEnding(body, span, doc.Newline)
	headingHasNewline := span.HeadingEnd > span.HeadingStart && body[span.HeadingEnd-1] == '\n'
	replacement := sectionReplacement(content, newline)
	if !headingHasNewline {
		replacement = newline + replacement
	}
	candidate := body[:span.ContentStart] + replacement + body[span.ContentEnd:]
	if errors := repairableBodyErrors(scanMarkdown(candidate)); len(errors) > 0 {
		return "", fmt.Errorf("section update would create an invalid feature body: %s", strings.Join(errors, "; "))
	}
	return candidate, nil
}

func sectionLineEnding(body string, span sectionSpan, fallback string) string {
	if span.HeadingEnd > span.HeadingStart && body[span.HeadingEnd-1] == '\n' {
		if span.HeadingEnd-span.HeadingStart >= 2 && body[span.HeadingEnd-2] == '\r' {
			return "\r\n"
		}
		return "\n"
	}
	return fallback
}

func addMissingSections(body string, requestedOrder []string, defaults map[string]string) (string, bool, error) {
	original := body
	body, boundaryRepaired := repairLegacyLinksBoundary(body)
	doc := scanMarkdown(body)
	if errors := repairableBodyErrors(doc); len(errors) > 0 {
		return body, false, fmt.Errorf("invalid feature body: %s", strings.Join(errors, "; "))
	}
	changed := boundaryRepaired
	for _, rawName := range requestedOrder {
		name := strings.TrimSpace(rawName)
		if name == "" {
			continue
		}
		doc = scanMarkdown(body)
		matches := sectionsNamed(doc, name)
		if len(matches) > 1 {
			return body, changed, fmt.Errorf("section %q appears %d times", name, len(matches))
		}
		if len(matches) == 1 {
			continue
		}

		position, err := missingSectionPosition(doc, name, requestedOrder)
		if err != nil {
			return body, changed, err
		}
		newline := doc.Newline
		prefix := insertionPrefix(body[:position], newline)
		content := ""
		if defaults != nil {
			content = defaults[name]
		}
		block := prefix + "## " + name + newline + sectionReplacement(content, newline)
		body = body[:position] + block + body[position:]
		changed = true
	}
	if errors := repairableBodyErrors(scanMarkdown(body)); len(errors) > 0 {
		return original, false, fmt.Errorf("section insertion would create an invalid feature body: %s", strings.Join(errors, "; "))
	}
	return body, changed, nil
}

// repairLegacyLinksBoundary recognizes the single historical template shape
// where the closing trust marker immediately preceded the final Links section.
// It performs only that unambiguous migration; arbitrary misplaced markers or
// content outside the boundary continue to fail closed.
func repairLegacyLinksBoundary(body string) (string, bool) {
	doc := scanMarkdown(body)
	if len(doc.Open) != 1 || len(doc.Close) != 1 || doc.Open[0].Start >= doc.Close[0].Start {
		return body, false
	}
	links := sectionsNamed(doc, "Links")
	if len(links) != 1 || links[0].HeadingStart <= doc.Close[0].End || len(doc.Sections) == 0 || doc.Sections[len(doc.Sections)-1].Name != "Links" {
		return body, false
	}
	if strings.TrimSpace(body[doc.Close[0].End:links[0].HeadingStart]) != "" {
		return body, false
	}
	for _, canonical := range canonicalSectionOrder[:len(canonicalSectionOrder)-1] {
		matches := sectionsNamed(doc, canonical)
		if len(matches) > 1 {
			return body, false
		}
		if len(matches) == 1 && (matches[0].HeadingStart <= doc.Open[0].Start || matches[0].HeadingStart >= doc.Close[0].Start) {
			return body, false
		}
	}

	marker := body[doc.Close[0].Start:doc.Close[0].End]
	repaired := body[:doc.Close[0].Start] + body[doc.Close[0].End:]
	separator := ""
	if !strings.HasSuffix(repaired, doc.Newline+doc.Newline) {
		if strings.HasSuffix(repaired, doc.Newline) {
			separator = doc.Newline
		} else {
			separator = doc.Newline + doc.Newline
		}
	}
	return repaired + separator + marker, true
}

func missingSectionPosition(doc markdownDocument, name string, requestedOrder []string) (int, error) {
	if rank, canonical := canonicalRank(name); canonical {
		for _, next := range canonicalSectionOrder[rank+1:] {
			if matches := sectionsNamed(doc, next); len(matches) == 1 {
				return matches[0].HeadingStart, nil
			}
		}
		if len(doc.Close) != 1 {
			return 0, fmt.Errorf("cannot insert canonical section %q without one closing trust boundary", name)
		}
		return doc.Close[0].Start, nil
	}

	requestedIndex := -1
	for index, requested := range requestedOrder {
		if strings.TrimSpace(requested) == name {
			requestedIndex = index
			break
		}
	}
	if requestedIndex >= 0 {
		for _, next := range requestedOrder[requestedIndex+1:] {
			if matches := sectionsNamed(doc, strings.TrimSpace(next)); len(matches) == 1 {
				return matches[0].HeadingStart, nil
			}
		}
	}
	if len(doc.Close) == 1 {
		return doc.Close[0].Start, nil
	}
	return 0, fmt.Errorf("cannot insert section %q without one closing trust boundary", name)
}

func insertionPrefix(prefix, newline string) string {
	if prefix == "" || strings.HasSuffix(prefix, newline+newline) {
		return ""
	}
	if strings.HasSuffix(prefix, newline) {
		return newline
	}
	return newline + newline
}

// ExtractSections exposes the fence-aware section order and defaults for
// external packages. Duplicate names retain the first occurrence.
func ExtractSections(body string) (order []string, defaults map[string]string) {
	doc := scanMarkdown(body)
	defaults = make(map[string]string, len(doc.Sections))
	for _, section := range doc.Sections {
		if _, exists := defaults[section.Name]; exists {
			continue
		}
		order = append(order, section.Name)
		defaults[section.Name] = strings.TrimSpace(body[section.ContentStart:section.ContentEnd])
	}
	return order, defaults
}
