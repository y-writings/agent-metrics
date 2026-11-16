package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"agent-metrics"},
		{"agent-metrics", "collect"},
		{"agent-metrics", "collect", "--product", "codex"},
		{"agent-metrics", "collect", "--agent", "agent"},
		{"agent-metrics", "collect", "--product", "codex", "--agent", "agent", "--unknown"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Fatalf("%v exit %d, want 2; stderr %s", args, code, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("usage errors must not write a report: %s", stdout.String())
		}
	}
}

func TestRunRejectsInvalidSourceWithoutFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configured := filepath.Join(t.TempDir(), "configured")
	sessions := filepath.Join(configured, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, "marker.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defaultSessions := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(defaultSessions, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultSessions, "marker.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", configured)

	var stdout, stderr bytes.Buffer
	code := run([]string{"agent-metrics", "collect", "--product", "codex", "--agent", "/root/capture_target", "--records", filepath.Join(t.TempDir(), "missing")}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr %s", code, stderr.String())
	}
	decoded := decodeReport(t, stdout.Bytes())
	if decoded["completeness"] != "incomplete" {
		t.Fatalf("report: %s", stdout.String())
	}
	diagnostics := decoded["diagnostics"].([]any)
	if len(diagnostics) != 1 || diagnostics[0].(map[string]any)["code"] != "invalid_source" || diagnostics[0].(map[string]any)["scope"] != "source" {
		t.Fatalf("diagnostics: %s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("resolution failure must not copy errors to stderr: %s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	t.Setenv("CODEX_HOME", "")
	code = run([]string{"agent-metrics", "collect", "--product", "codex", "--agent", "/root/capture_target"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("empty config exit %d", code)
	}
	decoded = decodeReport(t, stdout.Bytes())
	diagnostics = decoded["diagnostics"].([]any)
	if diagnostics[0].(map[string]any)["code"] != "invalid_source" || diagnostics[0].(map[string]any)["message"] != "CODEX_HOME is empty" {
		t.Fatalf("empty configuration fell through or was misclassified: %s", stdout.String())
	}
}

func TestCodexCLIMeasuresReturnedPath(t *testing.T) {
	records := filepath.Join(moduleRoot(t), "testdata/codex/native/records/r1/sessions")
	stdout, code := collectCLI(t, "codex", "/root/capture_target", "", records)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stdout)
	}
	decoded := decodeReport(t, []byte(stdout))
	if decoded["completeness"] != "complete" || decoded["status"] != "completed" {
		t.Fatalf("report: %s", stdout)
	}
	if usageTotal(t, decoded, "self") != 40080 || usageTotal(t, decoded, "subtree") != 40080 || elapsedOf(t, decoded) != 8994 {
		t.Fatalf("metrics: %s", stdout)
	}
}

func TestCodexCLISelectsReusedExecution(t *testing.T) {
	records := filepath.Join(moduleRoot(t), "testdata/codex/native/records/default/sessions")
	stdout, code := collectCLI(t, "codex", "/root/capture_target", "", records)
	if code != 1 {
		t.Fatalf("ambiguous reuse exit %d\n%s", code, stdout)
	}
	decoded := decodeReport(t, []byte(stdout))
	diagnostics := decoded["diagnostics"].([]any)
	if decoded["completeness"] != "incomplete" || diagnostics[0].(map[string]any)["code"] != "ambiguous_execution" {
		t.Fatalf("ambiguous report: %s", stdout)
	}
	stdout, code = collectCLI(t, "codex", "/root/capture_target", "b16afe9d-0ec9-7c62-80d5-b12717dbd878", records)
	if code != 0 {
		t.Fatalf("selected execution exit %d\n%s", code, stdout)
	}
	decoded = decodeReport(t, []byte(stdout))
	if usageTotal(t, decoded, "self") != 82419 || usageTotal(t, decoded, "subtree") != 122782 || elapsedOf(t, decoded) != 43699 {
		t.Fatalf("T.B metrics: %s", stdout)
	}
}

func TestClaudeCLIMeasuresReturnedAgent(t *testing.T) {
	root := moduleRoot(t)
	records := filepath.Join(root, "testdata/claude-code/native/records/r1/projects")
	stdout, code := collectCLI(t, "claude-code", "ada3d4768bc8b8ec8", "", records)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stdout)
	}
	decoded := decodeReport(t, []byte(stdout))
	if usageTotal(t, decoded, "self") != 25414 || elapsedOf(t, decoded) != 5437 || decoded["status"] != "completed" {
		t.Fatalf("metrics: %s", stdout)
	}
	self := decoded["usage"].(map[string]any)["self"].([]any)
	if self[0].(map[string]any)["provider"] != nil {
		t.Fatalf("provider must be null: %s", stdout)
	}
	stdout, code = collectCLI(t, "claude-code", "ada3d4768bc8b8ec8", "", filepath.Join(root, "testdata/claude-code/native/records/default/projects"))
	if code != 1 {
		t.Fatalf("ambiguous exit %d\n%s", code, stdout)
	}
	stdout, code = collectCLI(t, "claude-code", "ada3d4768bc8b8ec8", "b3d9191c-6c78-4c2d-a207-cdb53f2ea0a8", filepath.Join(root, "testdata/claude-code/native/records/default/projects"))
	if code != 0 {
		t.Fatalf("selected exit %d\n%s", code, stdout)
	}
	decoded = decodeReport(t, []byte(stdout))
	if usageTotal(t, decoded, "self") != 60121 || usageTotal(t, decoded, "subtree") != 85659 || elapsedOf(t, decoded) != 14826 {
		t.Fatalf("T.B metrics: %s", stdout)
	}
}

func TestOpenCodeCLIMeasuresReturnedSession(t *testing.T) {
	records := filepath.Join(moduleRoot(t), "testdata/opencode-v2/native/records/r1.db")
	stdout, code := collectCLI(t, "opencode-v2", "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", records)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stdout)
	}
	decoded := decodeReport(t, []byte(stdout))
	self := decoded["usage"].(map[string]any)["self"].([]any)[0].(map[string]any)["tokens"].(map[string]any)
	if self["input"].(float64) != 10200 || self["output"].(float64) != 66 || self["total"] != nil || elapsedOf(t, decoded) != 10099 || decoded["status"] != "succeeded" {
		t.Fatalf("metrics: %s", stdout)
	}
}

func collectCLI(t *testing.T, product, agent, execution, records string) (string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args := []string{"agent-metrics", "collect", "--product", product, "--agent", agent, "--records", records}
	if execution != "" {
		args = append(args, "--execution", execution)
	}
	code := run(args, &stdout, &stderr)
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %s", stderr.String())
	}
	return stdout.String(), code
}

func usageTotal(t *testing.T, report map[string]any, scope string) float64 {
	t.Helper()
	usage := report["usage"].(map[string]any)[scope].([]any)
	var sum float64
	for _, item := range usage {
		tokens := item.(map[string]any)["tokens"].(map[string]any)
		sum += tokens["total"].(float64)
	}
	return sum
}

func elapsedOf(t *testing.T, report map[string]any) float64 {
	t.Helper()
	value := report["timing"].(map[string]any)["elapsed_milliseconds"]
	if value == nil {
		t.Fatal("elapsed is null")
	}
	return value.(float64)
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func decodeReport(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode report: %v\n%s", err, raw)
	}
	return decoded
}
