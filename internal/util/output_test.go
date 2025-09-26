package util

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPrintJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := PrintJSON(&buf, map[string]string{"hello": "world"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded["hello"] != "world" {
		t.Fatalf("unexpected value: %#v", decoded)
	}
}

func TestStructuredResultAndPrintLines(t *testing.T) {
	payload := StructuredResult(true, "ok", map[string]int{"value": 1})
	if payload["success"] != true {
		t.Fatalf("expected success true: %#v", payload)
	}
	if payload["message"] != "ok" {
		t.Fatalf("unexpected message: %#v", payload)
	}
	if payload["data"].(map[string]int)["value"] != 1 {
		t.Fatalf("unexpected data")
	}

	var buf bytes.Buffer
	PrintLines(&buf, "one", "two")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Fatalf("unexpected lines: %#v", lines)
	}
}
