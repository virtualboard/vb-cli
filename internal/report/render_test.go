package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func source(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRenderIncludesNestedListsAndEscaping(t *testing.T) {
	root := source(t, map[string]string{
		"report.html":          `{{INCLUDE: _partials/title.html}}<ul>{{#ROWS}}<li title="{{TITLE}}">{{TITLE}}:{{PROJECT_NAME}}{{#DETAILS}}<b>{{VALUE}}</b>{{/DETAILS}}</li>{{/ROWS}}</ul>`,
		"_partials/title.html": `<!-- Documented {{MISSING}} ignored --><h1>{{TITLE}}</h1>`,
	})
	data, err := Decode(strings.NewReader(`{"TITLE":"<script>x</script>","ROWS":[{"TITLE":"\" onclick=\"alert(1)","DETAILS":[{"VALUE":"<&>"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	first, err := Render(root, "report.html", data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Render(root, "report.html", data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("rendering is not deterministic")
	}
	if strings.Contains(string(first), "<script>") || strings.Contains(string(first), `title="" onclick=`) {
		t.Fatalf("unescaped values: %s", first)
	}
	for _, want := range []string{"&lt;script&gt;", "&#34; onclick=&#34;alert(1)", "VirtualBoard", "&lt;&amp;&gt;"} {
		if !strings.Contains(string(first), want) {
			t.Fatalf("missing %q in %s", want, first)
		}
	}
}

func TestRenderContextualEscapingAndExplicitHTML(t *testing.T) {
	root := source(t, map[string]string{"report.html": `<a href="{{URL}}">{{TEXT_HTML}}</a><script>const RISKS = {{RISKS_JSON}};</script>`})
	data, err := Decode(strings.NewReader(`{"URL":"javascript:alert(1)","TEXT_HTML":{"html":"<em>reviewed</em>"},"RISKS_JSON":[{"title":"</script><script>alert(1)</script>"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render(root, "report.html", data, Options{}); err == nil {
		t.Fatal("raw HTML did not require opt-in")
	}
	raw, err := Render(root, "report.html", data, Options{AllowHTML: true})
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	if !strings.Contains(html, `href="#ZgotmplZ"`) || !strings.Contains(html, "<em>reviewed</em>") {
		t.Fatalf("escaping/HTML opt-in failed: %s", raw)
	}
	if strings.Count(html, "</script>") != 1 || !strings.Contains(html, `\u003c/script\u003e`) {
		t.Fatalf("JSON escaped its script context: %s", raw)
	}
	start := strings.Index(html, "const RISKS = ") + len("const RISKS = ")
	end := strings.Index(html[start:], ";")
	var risks []map[string]string
	if err := json.Unmarshal([]byte(html[start:start+end]), &risks); err != nil {
		t.Fatal(err)
	}
	if risks[0]["title"] != "</script><script>alert(1)</script>" {
		t.Fatal("JSON data changed")
	}
	data["TEXT_HTML"] = "<em>untrusted string</em>"
	raw, err = Render(root, "report.html", data, Options{AllowHTML: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "<em>untrusted") {
		t.Fatal("HTML suffix bypassed escaping")
	}
}

func TestRenderRejectsInvalidTemplateAndData(t *testing.T) {
	cases := []struct{ name, template, data string }{
		{"missing", "{{TITLE}}", `{}`},
		{"wrong list", "{{#ROWS}}{{TITLE}}{{/ROWS}}", `{"ROWS":"wrong"}`},
		{"wrong item", "{{#ROWS}}{{TITLE}}{{/ROWS}}", `{"ROWS":[1]}`},
		{"missing item field", "{{#ROWS}}{{TITLE}}{{/ROWS}}", `{"ROWS":[{}]}`},
		{"unclosed list", "{{#ROWS}}", `{"ROWS":[]}`},
		{"mismatched list", "{{#ROWS}}{{/OTHER}}", `{"ROWS":[]}`},
		{"unopened list", "{{/ROWS}}", `{}`},
		{"code injection", "{{printf \"oops\"}}", `{}`},
		{"unterminated", "{{TITLE", `{}`},
		{"invalid scalar type", "{{TITLE}}", `{"TITLE":[]}`},
		{"invalid key", "{{TITLE}}", `{"title":"x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := source(t, map[string]string{"report.html": tc.template})
			data, err := Decode(strings.NewReader(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Render(root, "report.html", data, Options{}); err == nil {
				t.Fatal("invalid report accepted")
			}
		})
	}
}

func TestRenderConfinesIncludesAndRejectsCycles(t *testing.T) {
	for _, include := range []string{"../outside.html", "/etc/passwd", `_partials\outside.html`, "report.html"} {
		t.Run(include, func(t *testing.T) {
			root := source(t, map[string]string{"report.html": "{{INCLUDE: " + include + "}}"})
			if _, err := Render(root, "report.html", nil, Options{}); err == nil {
				t.Fatal("unsafe include accepted")
			}
		})
	}
	outside := source(t, map[string]string{"outside.html": "secret"})
	root := source(t, map[string]string{"report.html": "{{INCLUDE: external.html}}"})
	if err := os.Symlink(filepath.Join(outside, "outside.html"), filepath.Join(root, "external.html")); err != nil {
		t.Skip(err)
	}
	if _, err := Render(root, "report.html", nil, Options{}); err == nil {
		t.Fatal("symlink escaped template root")
	}
}

func TestDecodeRejectsAmbiguousOrOversizedInput(t *testing.T) {
	for _, input := range []string{`{"A":1,"A":2}`, `{"RISKS_JSON":{"a":1,"a":2}}`, `{} {}`, `[]`, `null`, `{"A":`, strings.Repeat("[", 66) + strings.Repeat("]", 66), `{"A":"` + strings.Repeat("x", MaxInput) + `"}`} {
		if _, err := Decode(strings.NewReader(input)); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}

func TestRenderEmptyListsNullAndBrandDefaults(t *testing.T) {
	root := source(t, map[string]string{"report.html": `<p>{{BRAND_NAME}} {{BRAND_TAGLINE}} {{OPTIONAL}}</p>{{#ROWS}}missing:{{TITLE}}{{/ROWS}}`})
	data, err := Decode(strings.NewReader(`{"OPTIONAL":null,"ROWS":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := Render(root, "report.html", data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "<p>Astucia AI Development Studio </p>" {
		t.Fatalf("unexpected output: %s", raw)
	}
}

func TestBrandLogoAllowsRasterDataOnly(t *testing.T) {
	root := source(t, map[string]string{"report.html": `<img src="{{BRAND_LOGO_DATAURI}}">`, "_partials/astucia-logo.b64.txt": "data:image/jpeg;base64,aGVsbG8=\n"})
	raw, err := Render(root, "report.html", nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `src="data:image/jpeg;base64,aGVsbG8="`) {
		t.Fatalf("brand logo not preserved: %s", raw)
	}
	for _, logo := range []string{"data:image/svg+xml;base64,aGVsbG8=", "data:text/html;base64,aGVsbG8=", "data:image/png;base64,invalid!"} {
		if _, err := Render(root, "report.html", map[string]any{"BRAND_LOGO_DATAURI": logo}, Options{}); err == nil {
			t.Fatal("unsafe logo accepted")
		}
	}
}

func TestExpansionIsBoundedEvenWithoutOutput(t *testing.T) {
	root := source(t, map[string]string{"report.html": strings.Repeat("{{#ROWS}}", 8) + strings.Repeat("{{/ROWS}}", 8)})
	rows := make([]any, 100)
	for i := range rows {
		rows[i] = map[string]any{}
	}
	if _, err := Render(root, "report.html", map[string]any{"ROWS": rows}, Options{}); err == nil || !strings.Contains(err.Error(), "operation budget") {
		t.Fatalf("unbounded expansion: %v", err)
	}
}

func TestWriteOutputPreservesExistingReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.html")
	if err := WriteOutput(path, []byte("first"), false); err != nil {
		t.Fatal(err)
	}
	if err := WriteOutput(path, []byte("second"), false); err == nil {
		t.Fatal("existing report replaced")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "first" {
		t.Fatal("old report changed")
	}
	if err := WriteOutput(path, []byte("second"), true); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil || string(raw) != "second" {
		t.Fatal("explicit overwrite did not publish")
	}
}

func TestConcurrentReportPublicationHasOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.html")
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { results <- WriteOutput(path, []byte("complete artifact"), false) }()
	}
	winners := 0
	for i := 0; i < 8; i++ {
		if <-results == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d writers published the same destination", winners)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "complete artifact" {
		t.Fatalf("incomplete artifact: %q, %v", raw, err)
	}
}
