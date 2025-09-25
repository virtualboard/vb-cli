package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/config"
)

func TestRespondPlainAndJSON(t *testing.T) {
	opts := config.New()
	opts.JSONOutput = false
	config.SetCurrent(opts)

	command := &cobra.Command{}
	var buf bytes.Buffer
	command.SetOut(&buf)
	if err := respond(command, opts, true, "hello", nil); err != nil {
		t.Fatalf("respond failed: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("hello")) {
		t.Fatalf("expected plain output")
	}

	jsonOpts := config.New()
	jsonOpts.JSONOutput = true
	command.SetOut(&buf)
	buf.Reset()
	if err := respond(command, jsonOpts, true, "msg", map[string]int{"v": 1}); err != nil {
		t.Fatalf("respond json failed: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if payload["message"].(string) != "msg" {
		t.Fatalf("unexpected payload: %#v", payload)
	}

	config.SetCurrent(nil)
	if _, err := options(); err == nil {
		t.Fatalf("expected error when config not set")
	}
}
