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

func decodeReport(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode report: %v\n%s", err, raw)
	}
	return decoded
}
