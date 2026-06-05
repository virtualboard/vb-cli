package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/virtualboard/vb-cli/internal/audit"
	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/testutil"
)

// writeAuditLog seeds .virtualboard/audit.jsonl with a hash-chained set of entries.
func writeAuditLog(t *testing.T, fix *testutil.Fixture) string {
	t.Helper()
	workspace := filepath.Join(fix.Root, ".virtualboard")
	path := filepath.Join(workspace, "audit.jsonl")
	logger, err := audit.NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	seed := []struct {
		action, actor, fid, details string
	}{
		{"create", "alice", "FTR-1", "first feature"},
		{"lock", "alice", "FTR-1", "owner=alice ttl=1"},
		{"unlock", "alice", "FTR-1", "owner=alice"},
		{"move", "bob", "FTR-1", "from=backlog to=in-progress"},
		{"create", "carol", "FTR-2", ""},
	}
	for _, e := range seed {
		if err := logger.Log(e.action, e.actor, e.fid, e.details); err != nil {
			t.Fatalf("Log failed: %v", err)
		}
	}
	return path
}

func runAudit(t *testing.T, fix *testutil.Fixture, opts *config.Options, args ...string) string {
	t.Helper()
	config.SetCurrent(opts)
	cmd := newAuditCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("audit %v failed: %v\noutput:\n%s", args, err, buf.String())
	}
	return buf.String()
}

func TestAuditCommandHumanDefault(t *testing.T) {
	fix := testutil.NewFixture(t)
	writeAuditLog(t, fix)
	opts, _ := setupOptions(t, fix, false, false, false)
	out := runAudit(t, fix, opts)
	// All five seeded actions should appear in default human output.
	for _, action := range []string{"create", "lock", "unlock", "move"} {
		if !strings.Contains(out, action) {
			t.Fatalf("expected %q in human output, got:\n%s", action, out)
		}
	}
	// Hashes should be hidden by default.
	if strings.Contains(out, "prev=") {
		t.Fatalf("default human output should not include hashes:\n%s", out)
	}
}

func TestAuditCommandTableAndAgentAndXML(t *testing.T) {
	fix := testutil.NewFixture(t)
	writeAuditLog(t, fix)
	opts, _ := setupOptions(t, fix, false, false, false)

	tableOut := runAudit(t, fix, opts, "--format", "table")
	if !strings.Contains(tableOut, "TIMESTAMP") {
		t.Fatalf("table missing header:\n%s", tableOut)
	}

	agentOut := runAudit(t, fix, opts, "--format", "agent")
	if !strings.Contains(agentOut, "count: 5") || !strings.Contains(agentOut, "--- entry 1 ---") {
		t.Fatalf("agent output wrong:\n%s", agentOut)
	}

	xmlOut := runAudit(t, fix, opts, "--format", "xml")
	if !strings.Contains(xmlOut, "<audit") || !strings.Contains(xmlOut, "<entry>") {
		t.Fatalf("xml output wrong:\n%s", xmlOut)
	}

	jsonlOut := runAudit(t, fix, opts, "--format", "jsonl")
	if strings.Count(jsonlOut, "\n") != 5 {
		t.Fatalf("expected 5 jsonl lines, got:\n%s", jsonlOut)
	}
}

func TestAuditCommandFilters(t *testing.T) {
	fix := testutil.NewFixture(t)
	writeAuditLog(t, fix)
	opts, _ := setupOptions(t, fix, false, false, false)

	// --action lock should yield exactly one row.
	out := runAudit(t, fix, opts, "--format", "jsonl", "--action", "lock")
	if strings.Count(strings.TrimRight(out, "\n"), "\n")+1 != 1 {
		t.Fatalf("expected 1 lock entry, got:\n%s", out)
	}
	if !strings.Contains(out, "\"action\":\"lock\"") {
		t.Fatalf("expected lock entry in output:\n%s", out)
	}

	// --actor alice: 3 entries.
	out = runAudit(t, fix, opts, "--format", "jsonl", "--actor", "alice")
	if got := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; got != 3 {
		t.Fatalf("expected 3 alice entries, got %d:\n%s", got, out)
	}

	// --feature-id FTR-2: 1 entry.
	out = runAudit(t, fix, opts, "--format", "jsonl", "--feature-id", "FTR-2")
	if got := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; got != 1 {
		t.Fatalf("expected 1 FTR-2 entry, got %d:\n%s", got, out)
	}

	// --contains "ttl" matches the lock entry only.
	out = runAudit(t, fix, opts, "--format", "jsonl", "--contains", "TTL")
	if got := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; got != 1 {
		t.Fatalf("expected 1 contains-match, got %d:\n%s", got, out)
	}

	// --limit 2 yields first two entries.
	out = runAudit(t, fix, opts, "--format", "jsonl", "--limit", "2")
	if got := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; got != 2 {
		t.Fatalf("expected 2 head entries, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "\"action\":\"create\"") {
		t.Fatalf("expected first entry to be create:\n%s", out)
	}

	// --limit 2 --tail yields last two entries.
	out = runAudit(t, fix, opts, "--format", "jsonl", "--limit", "2", "--tail")
	if !strings.Contains(out, "\"feature_id\":\"FTR-2\"") {
		t.Fatalf("expected tail to include last entry:\n%s", out)
	}
}

func TestAuditCommandJSONOutput(t *testing.T) {
	fix := testutil.NewFixture(t)
	writeAuditLog(t, fix)
	opts, _ := setupOptions(t, fix, true, false, false)
	out := runAudit(t, fix, opts, "--format", "table") // --format must be ignored
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Path    string        `json:"path"`
			Total   int           `json:"total"`
			Count   int           `json:"count"`
			Entries []audit.Entry `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("JSON output not parseable: %v\n%s", err, out)
	}
	if !payload.Success || payload.Data.Count != 5 || payload.Data.Total != 5 {
		t.Fatalf("unexpected JSON payload: %+v", payload.Data)
	}
}

func TestAuditCommandVerifySuccess(t *testing.T) {
	fix := testutil.NewFixture(t)
	writeAuditLog(t, fix)
	opts, _ := setupOptions(t, fix, false, false, false)
	out := runAudit(t, fix, opts, "--verify")
	if !strings.Contains(out, "Audit chain verified: OK") {
		t.Fatalf("expected verification confirmation, got:\n%s", out)
	}
}

func TestAuditCommandVerifyFailure(t *testing.T) {
	fix := testutil.NewFixture(t)
	path := writeAuditLog(t, fix)

	// Corrupt the second line's entry_hash to break the chain.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	lines[1] = strings.Replace(lines[1], "\"entry_hash\":\"", "\"entry_hash\":\"00", 1)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts, _ := setupOptions(t, fix, false, false, false)
	config.SetCurrent(opts)
	cmd := newAuditCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--verify"})
	err = cmd.Execute()
	if err == nil {
		t.Fatalf("expected verify failure to return error")
	}
	if ExitCode(err) != ExitCodeValidation {
		t.Fatalf("expected ExitCodeValidation, got %d", ExitCode(err))
	}
}

func TestAuditCommandInvalidFormat(t *testing.T) {
	fix := testutil.NewFixture(t)
	writeAuditLog(t, fix)
	opts, _ := setupOptions(t, fix, false, false, false)
	config.SetCurrent(opts)
	cmd := newAuditCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--format", "yaml"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for unknown format")
	} else if ExitCode(err) != ExitCodeValidation {
		t.Fatalf("expected ExitCodeValidation, got %d", ExitCode(err))
	}
}

func TestAuditCommandInvalidTimeFlags(t *testing.T) {
	fix := testutil.NewFixture(t)
	writeAuditLog(t, fix)

	for _, flag := range []string{"--since", "--until"} {
		t.Run(flag, func(t *testing.T) {
			opts, _ := setupOptions(t, fix, false, false, false)
			config.SetCurrent(opts)
			cmd := newAuditCommand()
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs([]string{flag, "yesterday"})
			if err := cmd.Execute(); err == nil {
				t.Fatalf("expected error for invalid time on %s", flag)
			} else if ExitCode(err) != ExitCodeValidation {
				t.Fatalf("expected ExitCodeValidation, got %d", ExitCode(err))
			}
		})
	}
}

func TestAuditCommandTimeRange(t *testing.T) {
	fix := testutil.NewFixture(t)
	writeAuditLog(t, fix)
	opts, _ := setupOptions(t, fix, false, false, false)

	// Wide window catches everything.
	out := runAudit(t, fix, opts, "--format", "jsonl", "--since", "1970-01-01", "--until", "2999-01-01")
	if got := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; got != 5 {
		t.Fatalf("expected all 5 entries in wide window, got %d", got)
	}

	// Window in the distant future filters everything out.
	out = runAudit(t, fix, opts, "--format", "jsonl", "--since", "2999-01-01")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty output for future window, got:\n%s", out)
	}
}

func TestAuditCommandMissingFile(t *testing.T) {
	fix := testutil.NewFixture(t)
	// No audit log written.
	opts, _ := setupOptions(t, fix, false, false, false)
	out := runAudit(t, fix, opts)
	if !strings.Contains(out, "No audit entries") {
		t.Fatalf("expected empty-state message, got %q", out)
	}
}

func TestAuditCommandVerboseHashes(t *testing.T) {
	fix := testutil.NewFixture(t)
	writeAuditLog(t, fix)
	opts, _ := setupOptions(t, fix, false, true, false)
	out := runAudit(t, fix, opts, "--format", "human")
	if !strings.Contains(out, "prev=") || !strings.Contains(out, "hash=") {
		t.Fatalf("verbose human output should show hashes:\n%s", out)
	}
}

func TestAuditCommandVerboseLogsParseErrors(t *testing.T) {
	fix := testutil.NewFixture(t)
	path := writeAuditLog(t, fix)
	// Append a malformed line.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not a json line\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	opts, _ := setupOptions(t, fix, false, true, false)
	out := runAudit(t, fix, opts)
	// Valid entries still render.
	if !strings.Contains(out, "create") {
		t.Fatalf("expected valid entries to still render, got:\n%s", out)
	}
}
