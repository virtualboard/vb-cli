package util

import (
	"encoding/json"
	"fmt"
	"io"
)

// PrintJSON writes the provided value as indented JSON.
func PrintJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// StructuredResult provides a consistent payload for JSON responses.
func StructuredResult(success bool, message string, data interface{}) map[string]interface{} {
	payload := map[string]interface{}{
		"success": success,
	}
	if message != "" {
		payload["message"] = message
	}
	if data != nil {
		payload["data"] = data
	}
	return payload
}

// PrintLines prints each string on a new line.
func PrintLines(w io.Writer, lines ...string) {
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
}
