// Package report renders VirtualBoard's Mustache-style HTML reports through
// html/template. Template authors are trusted; report values are not.
package report

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

const (
	MaxInput    = 4 << 20
	maxTemplate = 16 << 20
	maxOutput   = 16 << 20
	maxDepth    = 32
)

var namePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
var comments = regexp.MustCompile(`(?s)<!--.*?-->`)

// Options controls the single opt-in escape bypass. A value must also use
// {"html":"..."}; a field's name alone never makes its content trusted.
type Options struct{ AllowHTML bool }

// Decode reads one JSON object, rejecting duplicate keys and excessive nesting.
func Decode(r io.Reader) (map[string]any, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxInput+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxInput {
		return nil, fmt.Errorf("report data exceeds %d bytes", MaxInput)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := decodeValue(dec, 0)
	if err != nil {
		return nil, fmt.Errorf("report data: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("report data must contain exactly one JSON object")
	}
	data, ok := value.(map[string]any)
	if !ok || data == nil {
		return nil, fmt.Errorf("report data must be a JSON object")
	}
	return data, nil
}

func decodeValue(dec *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("JSON nesting exceeds 64 levels")
	}
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return token, nil
	}
	switch delim {
	case '{':
		result := map[string]any{}
		for dec.More() {
			token, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := token.(string)
			if !ok {
				return nil, fmt.Errorf("object key must be a string")
			}
			if _, exists := result[key]; exists {
				return nil, fmt.Errorf("duplicate key %q", key)
			}
			value, err := decodeValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return result, nil
	case '[':
		result := []any{}
		for dec.More() {
			value, err := decodeValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

// Render resolves includes relative to root, then renders with contextual HTML,
// attribute, CSS, and JavaScript escaping. Missing values are errors: supply an
// explicit empty string, null, or empty list for an optional slot.
func Render(root, name string, data map[string]any, opts Options) ([]byte, error) {
	directory, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open report templates: %w", err)
	}
	// #nosec G104 -- closing a read-only directory handle cannot affect the artifact.
	defer directory.Close()
	loader := loader{root: directory, active: map[string]bool{}}
	source, err := loader.expand(name, 0)
	if err != nil {
		return nil, err
	}
	compiled, err := compile(source)
	if err != nil {
		return nil, err
	}
	values := make(map[string]any, len(data)+3)
	for key, value := range data {
		if !namePattern.MatchString(key) {
			return nil, fmt.Errorf("invalid report field %q: use uppercase names with underscores", key)
		}
		values[key] = value
	}
	defaults := map[string]any{"BRAND_NAME": "Astucia", "BRAND_TAGLINE": "AI Development Studio", "PROJECT_NAME": "VirtualBoard"}
	for key, value := range defaults {
		if _, ok := values[key]; !ok {
			values[key] = value
		}
	}
	// Do not require a logo asset for templates that never refer to it.
	if _, ok := values["BRAND_LOGO_DATAURI"]; !ok && strings.Contains(compiled, `"BRAND_LOGO_DATAURI"`) {
		raw, err := readFile(directory, "_partials/astucia-logo.b64.txt", MaxInput)
		if err != nil {
			return nil, fmt.Errorf("read default brand logo: %w", err)
		}
		values["BRAND_LOGO_DATAURI"] = strings.TrimSpace(string(raw))
	}
	renderer := renderer{options: opts}
	tpl, err := template.New("report").Funcs(template.FuncMap{"value": renderer.value, "items": renderer.items}).Parse(compiled)
	if err != nil {
		return nil, fmt.Errorf("parse report: %w", err)
	}
	var output boundedBuffer
	if err := tpl.Execute(&output, values); err != nil {
		return nil, fmt.Errorf("render report: %w", err)
	}
	return output.Bytes(), nil
}

type loader struct {
	root   *os.Root
	active map[string]bool
	size   int
}

func (l *loader) expand(name string, depth int) (string, error) {
	if depth > maxDepth {
		return "", fmt.Errorf("report includes exceed %d levels", maxDepth)
	}
	if !fs.ValidPath(name) || strings.ContainsAny(name, `\:`) {
		return "", fmt.Errorf("invalid report template path %q", name)
	}
	if l.active[name] {
		return "", fmt.Errorf("cyclic report include %q", name)
	}
	raw, err := readFile(l.root, name, maxTemplate-l.size)
	if err != nil {
		return "", fmt.Errorf("read report template %s: %w", name, err)
	}
	l.size += len(raw)
	l.active[name] = true
	defer delete(l.active, name)
	// Template comments describe fields and contain example placeholders. They
	// are documentation, not values to evaluate, and never enter the result.
	source := comments.ReplaceAllString(string(raw), "")
	var out strings.Builder
	for {
		start := strings.Index(source, "{{INCLUDE:")
		if start < 0 {
			out.WriteString(source)
			break
		}
		out.WriteString(source[:start])
		end := strings.Index(source[start+2:], "}}")
		if end < 0 {
			return "", fmt.Errorf("unterminated report include in %s", name)
		}
		end += start + 2
		path := strings.TrimSpace(source[start+len("{{INCLUDE:") : end])
		content, err := l.expand(path, depth+1)
		if err != nil {
			return "", err
		}
		out.WriteString(content)
		source = source[end+2:]
	}
	return out.String(), nil
}

func readFile(root *os.Root, name string, limit int) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	// #nosec G104 -- closing a read-only template handle is cleanup only.
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("template input size limit exceeded")
	}
	return raw, nil
}

func compile(source string) (string, error) {
	var out strings.Builder
	var stack []string
	count := 0
	for {
		start := strings.Index(source, "{{")
		if start < 0 {
			out.WriteString(source)
			break
		}
		out.WriteString(source[:start])
		source = source[start+2:]
		end := strings.Index(source, "}}")
		if end < 0 {
			return "", fmt.Errorf("unterminated report placeholder")
		}
		token := strings.TrimSpace(source[:end])
		source = source[end+2:]
		count++
		if count > 10000 {
			return "", fmt.Errorf("too many report placeholders")
		}
		name := strings.TrimLeft(token, "#/")
		if !namePattern.MatchString(name) {
			return "", fmt.Errorf("unsupported report placeholder %q", token)
		}
		switch {
		case strings.HasPrefix(token, "#"):
			if token != "#"+name {
				return "", fmt.Errorf("invalid list placeholder %q", token)
			}
			if len(stack) >= maxDepth {
				return "", fmt.Errorf("report lists exceed %d levels", maxDepth)
			}
			stack = append(stack, name)
			fmt.Fprintf(&out, `{{range items . %q}}`, name)
		case strings.HasPrefix(token, "/"):
			if token != "/"+name || len(stack) == 0 || stack[len(stack)-1] != name {
				return "", fmt.Errorf("unmatched report list %q", token)
			}
			stack = stack[:len(stack)-1]
			out.WriteString("{{end}}")
		default:
			fmt.Fprintf(&out, `{{value . %q}}`, name)
		}
	}
	if len(stack) > 0 {
		return "", fmt.Errorf("unclosed report list %q", stack[len(stack)-1])
	}
	return out.String(), nil
}

type renderer struct {
	options    Options
	operations int
}

func (r *renderer) value(data map[string]any, name string) (any, error) {
	if err := r.consume(1); err != nil {
		return nil, err
	}
	value, ok := data[name]
	if !ok {
		return nil, fmt.Errorf("missing report field %s", name)
	}
	if strings.HasSuffix(name, "_JSON") {
		return value, nil
	}
	if name == "BRAND_LOGO_DATAURI" {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be a string", name)
		}
		if strings.HasPrefix(text, "data:") {
			if err := validateLogo(text); err != nil {
				return nil, err
			}
			// #nosec G203 -- only verified base64 raster-image data URLs bypass URL filtering.
			return template.URL(text), nil
		}
	}
	switch value := value.(type) {
	case nil, string, bool, json.Number:
		return value, nil
	case map[string]any:
		html, ok := value["html"].(string)
		if len(value) == 1 && ok {
			if !r.options.AllowHTML {
				return nil, fmt.Errorf("%s contains trusted HTML; pass --allow-html only for reviewed fragments", name)
			}
			// #nosec G203 -- explicit caller opt-in and per-value {"html":...} annotation.
			return template.HTML(html), nil
		}
	}
	return nil, fmt.Errorf("report field %s must be a scalar or an explicitly trusted HTML fragment", name)
}

func validateLogo(value string) error {
	for _, format := range []string{"png", "jpeg", "gif", "webp"} {
		prefix := "data:image/" + format + ";base64,"
		if strings.HasPrefix(value, prefix) {
			if _, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, prefix)); err != nil {
				return fmt.Errorf("invalid base64 brand logo: %w", err)
			}
			return nil
		}
	}
	return errors.New("brand data URL must be a base64 PNG, JPEG, GIF, or WebP image")
}

func (r *renderer) items(data map[string]any, name string) ([]map[string]any, error) {
	value, ok := data[name]
	if !ok {
		return nil, fmt.Errorf("missing report list %s", name)
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("report list %s must be an array of objects", name)
	}
	if len(list) > 10000 {
		return nil, fmt.Errorf("report list %s exceeds 10000 items", name)
	}
	if err := r.consume(1 + len(list)*(len(data)+1)); err != nil {
		return nil, err
	}
	frames := make([]map[string]any, 0, len(list))
	for i, item := range list {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("report list %s item %d must be an object", name, i)
		}
		frame := make(map[string]any, len(data)+len(row))
		for key, value := range data {
			frame[key] = value
		}
		for key, value := range row {
			if !namePattern.MatchString(key) {
				return nil, fmt.Errorf("invalid report field %s[%d].%s", name, i, key)
			}
			frame[key] = value
		}
		frames = append(frames, frame)
	}
	return frames, nil
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > maxOutput-b.Len() {
		return 0, fmt.Errorf("rendered report exceeds %d bytes", maxOutput)
	}
	return b.Buffer.Write(p)
}

// Include/list expansion must remain bounded even when repeated blocks emit no bytes.
func (r *renderer) consume(count int) error {
	if count > 1000000-r.operations {
		return fmt.Errorf("report expansion exceeds operation budget")
	}
	r.operations += count
	return nil
}
