package util

import (
	"regexp"
	"strings"
)

var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify converts a string to kebab-case suitable for filenames.
func Slugify(input string) string {
	lower := strings.ToLower(input)
	slug := slugPattern.ReplaceAllString(lower, "-")
	slug = strings.Trim(slug, "-")
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return "feature"
	}
	return slug
}
