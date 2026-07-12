package frameworkschema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSchemaFixture(t *testing.T, root, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(root, "schemas", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCanonicalSchemasAuthenticateWorkspaceMirrors(t *testing.T) {
	root := t.TempDir()
	featurePath := writeSchemaFixture(t, root, "frontmatter.schema.json", CanonicalFeature())
	specPath := writeSchemaFixture(t, root, "system-spec.schema.json", CanonicalSystemSpec())
	if _, err := Feature(root, featurePath); err != nil {
		t.Fatalf("authenticate feature schema: %v", err)
	}
	if _, err := SystemSpec(root, specPath); err != nil {
		t.Fatalf("authenticate system-spec schema: %v", err)
	}
}

func TestWorkspaceSchemaTamperingFailsBeforeItCanResolveReferences(t *testing.T) {
	root := t.TempDir()
	path := writeSchemaFixture(t, root, "frontmatter.schema.json", []byte(`{"$ref":"https://attacker.invalid/schema.json"}`))
	if _, err := Feature(root, path); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("remote-reference schema tampering error = %v", err)
	}
	if err := os.WriteFile(path, append(CanonicalFeature(), []byte(`\n{"duplicate":true}`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Feature(root, path); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("trailing schema content error = %v", err)
	}
}

func TestWorkspaceSchemaRejectsLinksAndOversizeData(t *testing.T) {
	root := t.TempDir()
	target := writeSchemaFixture(t, root, "target.json", CanonicalFeature())
	link := filepath.Join(root, "schemas", "frontmatter.schema.json")
	if err := os.Symlink(target, link); err == nil {
		if _, err := Feature(root, link); err == nil {
			t.Fatal("symlinked workspace schema was accepted")
		}
	}
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, make([]byte, maxSchemaBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Feature(root, link); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized workspace schema error = %v", err)
	}
}
