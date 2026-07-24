package spec

import (
	"errors"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	specMarkdownReferencePattern = regexp.MustCompile(`(?m)^[\t ]{0,3}\[[^\r\n]*?\]:[\t ]*(?:<([^>\r\n]+)>|([^\s]+))`)
)

const maxSpecMarkdownLinks = 1024

func (v *Validator) validateInternalLinks(spec *Spec) []string {
	destinations, overflow := specMarkdownDestinations(spec.Body)
	if overflow {
		return []string{fmt.Sprintf("system spec body contains more than %d Markdown links", maxSpecMarkdownLinks)}
	}
	rootAbsolute, err := filepath.Abs(v.root)
	if err != nil {
		return []string{"cannot resolve workspace root for internal links: " + err.Error()}
	}
	realRoot, err := filepath.EvalSymlinks(rootAbsolute)
	if err != nil {
		return []string{"cannot resolve workspace root for internal links: " + err.Error()}
	}
	validationErrors := []string{}
	for _, rawDestination := range destinations {
		destination := normalizeSpecMarkdownDestination(specMarkdownDestination(rawDestination))
		if destination == "" || strings.HasPrefix(destination, "#") || strings.HasPrefix(destination, "//") {
			continue
		}
		parsed, err := url.Parse(destination)
		if err != nil {
			validationErrors = append(validationErrors, "invalid Markdown link destination: "+destination)
			continue
		}
		if parsed.Scheme != "" {
			if strings.EqualFold(parsed.Scheme, "file") {
				validationErrors = append(validationErrors, "local file link scheme is not allowed: "+destination)
			} else if specWindowsDrivePath(destination) {
				validationErrors = append(validationErrors, "internal link escapes workspace: "+destination)
			}
			continue
		}
		linkPath, err := url.PathUnescape(parsed.Path)
		if err != nil || linkPath == "" {
			continue
		}
		if specWindowsDrivePath(linkPath) || strings.HasPrefix(linkPath, `\`) {
			validationErrors = append(validationErrors, "internal link escapes workspace: "+destination)
			continue
		}
		var target string
		if filepath.IsAbs(filepath.FromSlash(linkPath)) {
			target = filepath.Join(rootAbsolute, strings.TrimLeft(filepath.FromSlash(linkPath), string(filepath.Separator)))
		} else {
			target = filepath.Join(filepath.Dir(spec.Path), filepath.FromSlash(linkPath))
		}
		target, err = filepath.Abs(filepath.Clean(target))
		if err != nil || !specPathContainedBy(rootAbsolute, target) {
			validationErrors = append(validationErrors, "internal link escapes workspace: "+destination)
			continue
		}
		realTarget, resolveErr := resolveSpecPathThroughExistingAncestor(target)
		if resolveErr != nil {
			validationErrors = append(validationErrors, "internal link target not found: "+destination)
			continue
		}
		if !specPathContainedBy(realRoot, realTarget) {
			validationErrors = append(validationErrors, "internal link resolves outside workspace: "+destination)
			continue
		}
		if _, err := os.Lstat(target); err != nil {
			validationErrors = append(validationErrors, "internal link target not found: "+destination)
		}
	}
	return validationErrors
}

func specMarkdownDestinations(body string) ([]string, bool) {
	source := specMarkdownLinkSource(body)
	destinations, overflow := specInlineMarkdownDestinations(source, maxSpecMarkdownLinks)
	if overflow {
		return nil, true
	}
	remaining := maxSpecMarkdownLinks - len(destinations)
	references := specMarkdownReferencePattern.FindAllStringSubmatch(source, remaining+1)
	if len(references) > remaining {
		return nil, true
	}
	for _, match := range references {
		if len(match) != 3 {
			continue
		}
		if match[1] != "" {
			destinations = append(destinations, match[1])
		} else if match[2] != "" {
			destinations = append(destinations, match[2])
		}
	}
	return destinations, false
}

func specInlineMarkdownDestinations(source string, limit int) ([]string, bool) {
	destinations := make([]string, 0)
	for cursor := 0; cursor < len(source); {
		relativeClose := strings.Index(source[cursor:], "](")
		if relativeClose < 0 {
			break
		}
		closeIndex := cursor + relativeClose
		cursor = closeIndex + 2
		if escapedSpecMarkdownByte(source, closeIndex) || !specHasOpeningLinkLabel(source, closeIndex) {
			continue
		}
		destination, next, ok := specInlineDestination(source, cursor)
		if !ok {
			continue
		}
		cursor = next
		destinations = append(destinations, destination)
		if len(destinations) > limit {
			return nil, true
		}
	}
	return destinations, false
}

func specHasOpeningLinkLabel(source string, closeIndex int) bool {
	lineStart := strings.LastIndexByte(source[:closeIndex], '\n') + 1
	for index := closeIndex - 1; index >= lineStart; index-- {
		if source[index] == '[' && !escapedSpecMarkdownByte(source, index) {
			return true
		}
	}
	return false
}

func specInlineDestination(source string, start int) (string, int, bool) {
	if start >= len(source) || source[start] == '\n' || source[start] == '\r' {
		return "", start, false
	}
	if source[start] == '<' {
		for index := start + 1; index < len(source); index++ {
			if source[index] == '\n' || source[index] == '\r' {
				return "", index, false
			}
			if source[index] == '>' && !escapedSpecMarkdownByte(source, index) {
				return source[start+1 : index], index + 1, true
			}
		}
		return "", len(source), false
	}
	depth := 0
	for index := start; index < len(source); index++ {
		if source[index] == '\n' || source[index] == '\r' {
			return "", index, false
		}
		if escapedSpecMarkdownByte(source, index) {
			continue
		}
		switch source[index] {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return source[start:index], index + 1, true
			}
			depth--
		case ' ', '\t':
			if depth == 0 {
				return source[start:index], index + 1, true
			}
		}
	}
	return "", len(source), false
}

func specMarkdownLinkSource(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var source strings.Builder
	fenceMarker := byte(0)
	fenceLength := 0
	for _, line := range lines {
		if marker, length, ok := specMarkdownFenceLine(line); ok {
			if fenceMarker == 0 {
				fenceMarker, fenceLength = marker, length
			} else if marker == fenceMarker && length >= fenceLength {
				fenceMarker, fenceLength = 0, 0
			}
			source.WriteByte('\n')
			continue
		}
		if fenceMarker != 0 {
			source.WriteByte('\n')
			continue
		}
		source.WriteString(stripSpecInlineCode(line))
		source.WriteByte('\n')
	}
	return source.String()
}

func specMarkdownFenceLine(line string) (byte, int, bool) {
	if strings.HasPrefix(line, "\t") {
		return 0, 0, false
	}
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return 0, 0, false
	}
	trimmed = strings.TrimSpace(trimmed)
	marker, length, ok := markdownFence(trimmed)
	if ok && marker == '`' && strings.Contains(trimmed[length:], "`") {
		return 0, 0, false
	}
	return marker, length, ok
}

func stripSpecInlineCode(line string) string {
	masked := []byte(line)
	for start := 0; start < len(line); {
		if line[start] != '`' || escapedSpecMarkdownByte(line, start) {
			start++
			continue
		}
		runLength := 1
		for start+runLength < len(line) && line[start+runLength] == '`' {
			runLength++
		}
		end := matchingSpecBacktickRun(line, start+runLength, runLength)
		if end < 0 {
			start += runLength
			continue
		}
		for index := start; index < end+runLength; index++ {
			masked[index] = ' '
		}
		start = end + runLength
	}
	return string(masked)
}

func escapedSpecMarkdownByte(line string, index int) bool {
	backslashes := 0
	for index > 0 && line[index-1] == '\\' {
		backslashes++
		index--
	}
	return backslashes%2 == 1
}

func matchingSpecBacktickRun(line string, start, wantLength int) int {
	for index := start; index < len(line); {
		if line[index] != '`' || escapedSpecMarkdownByte(line, index) {
			index++
			continue
		}
		runLength := 1
		for index+runLength < len(line) && line[index+runLength] == '`' {
			runLength++
		}
		if runLength == wantLength {
			return index
		}
		index += runLength
	}
	return -1
}

func specWindowsDrivePath(path string) bool {
	if len(path) < 3 || path[1] != ':' || (path[2] != '/' && path[2] != '\\') {
		return false
	}
	return path[0] >= 'A' && path[0] <= 'Z' || path[0] >= 'a' && path[0] <= 'z'
}

func specMarkdownDestination(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "<") {
		if end := strings.Index(raw, ">"); end > 0 {
			return raw[1:end]
		}
	}
	if fields := strings.Fields(raw); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func normalizeSpecMarkdownDestination(destination string) string {
	destination = html.UnescapeString(destination)
	var normalized strings.Builder
	normalized.Grow(len(destination))
	for index := 0; index < len(destination); index++ {
		if destination[index] == '\\' && index+1 < len(destination) && isSpecASCIIPunctuation(destination[index+1]) {
			index++
		}
		normalized.WriteByte(destination[index])
	}
	return normalized.String()
}

func isSpecASCIIPunctuation(value byte) bool {
	return value >= '!' && value <= '/' || value >= ':' && value <= '@' || value >= '[' && value <= '`' || value >= '{' && value <= '~'
}

func specPathContainedBy(root, target string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func resolveSpecPathThroughExistingAncestor(path string) (string, error) {
	current := filepath.Clean(path)
	missing := []string{}
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return "", resolveErr
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", os.ErrNotExist
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
