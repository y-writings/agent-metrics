package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestCollectNativeSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearSourceEnv(t)
	root := moduleRoot(t)
	copyTree(t, filepath.Join(root, "testdata/claude-code/native/records/r1/projects"), filepath.Join(home, ".claude/projects"))
	copyTree(t, filepath.Join(root, "testdata/codex/native/records/r1/sessions"), filepath.Join(home, ".codex/sessions"))
	copyFileTo(t, filepath.Join(root, "testdata/opencode-v2/native/records/r1.db"), filepath.Join(home, ".local/share/opencode/opencode.db"))

	stdout, code := collectCLI(t, "claude-code", "ada3d4768bc8b8ec8", "", "")
	if code != 0 {
		t.Fatalf("claude exit %d\n%s", code, stdout)
	}
	decoded := decodeReport(t, []byte(stdout))
	if tokenValue(t, decoded, "self", "total") != 25414 || elapsedOf(t, decoded) != 5437 || decoded["status"] != "completed" {
		t.Fatalf("claude home discovery: %s", stdout)
	}
	if decoded["usage"].(map[string]any)["self"].([]any)[0].(map[string]any)["provider"] != nil || !diagnosticCode(decoded, "unknown_provider") || decoded["schema_version"] != "2.1" {
		t.Fatalf("claude provider stays unknown in schema 2.1: %s", stdout)
	}

	stdout, code = collectCLI(t, "codex", "/root/capture_target", "", "")
	if code != 0 {
		t.Fatalf("codex exit %d\n%s", code, stdout)
	}
	decoded = decodeReport(t, []byte(stdout))
	if tokenValue(t, decoded, "self", "total") != 40080 || tokenValue(t, decoded, "subtree", "total") != 40080 || elapsedOf(t, decoded) != 8994 {
		t.Fatalf("codex home discovery: %s", stdout)
	}

	stdout, code = collectCLI(t, "opencode-v2", "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", "")
	if code != 0 {
		t.Fatalf("opencode exit %d\n%s", code, stdout)
	}
	decoded = decodeReport(t, []byte(stdout))
	if tokenValue(t, decoded, "self", "input") != 10200 || tokenValue(t, decoded, "self", "total") != -1 || elapsedOf(t, decoded) != 10099 || decoded["status"] != "succeeded" {
		t.Fatalf("opencode home discovery: %s", stdout)
	}
}

func TestCollectSourceOverrides(t *testing.T) {
	root := moduleRoot(t)
	base := t.TempDir()
	prepare := func(script string) string {
		t.Helper()
		destination := filepath.Join(base, strings.ReplaceAll(script, "/", "_"))
		command := exec.Command("python3", filepath.Join(root, script), destination)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("prepare %s: %v\n%s", script, err, output)
		}
		return destination
	}
	claude := prepare("testdata/claude-code/native/prepare.py")
	codex := prepare("testdata/codex/native/prepare.py")
	opencode := prepare("testdata/opencode-v2/native/prepare.py")
	clearSourceEnv(t)

	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(claude, "lookup/configured"))
	stdout, code := collectCLI(t, "claude-code", "ada3d4768bc8b8ec8", "", filepath.Join(claude, "lookup/explicit/projects"))
	if code != 0 || tokenValue(t, decodeReport(t, []byte(stdout)), "self", "total") != 25414 {
		t.Fatalf("explicit claude root: exit %d\n%s", code, stdout)
	}
	stdout, code = collectCLI(t, "claude-code", "a902a36167c8ba175", "", "")
	if code != 0 || tokenValue(t, decodeReport(t, []byte(stdout)), "self", "total") != 22597 {
		t.Fatalf("configured claude root: exit %d\n%s", code, stdout)
	}
	stdout, code = collectCLI(t, "claude-code", "ada3d4768bc8b8ec8", "", filepath.Join(t.TempDir(), "missing"))
	decoded := decodeReport(t, []byte(stdout))
	if code != 1 || !diagnosticCode(decoded, "invalid_source") || tokenValue(t, decoded, "self", "total") == 22597 {
		t.Fatalf("invalid explicit must not use the configured target: exit %d\n%s", code, stdout)
	}
	stdout, code = collectCLI(t, "claude-code", "ada3d4768bc8b8ec8", "", filepath.Join(claude, "lookup/no-target/projects"))
	decoded = decodeReport(t, []byte(stdout))
	if code != 1 || !diagnosticCode(decoded, "agent_not_found") {
		t.Fatalf("missing agent: exit %d\n%s", code, stdout)
	}
	os.Unsetenv("CLAUDE_CONFIG_DIR")

	t.Setenv("CODEX_HOME", filepath.Join(codex, "lookup/configured"))
	stdout, code = collectCLI(t, "codex", "/root/override_child", "", "")
	if code != 0 || tokenValue(t, decodeReport(t, []byte(stdout)), "self", "total") != 36174 || elapsedOf(t, decodeReport(t, []byte(stdout))) != 8130 {
		t.Fatalf("configured codex home: exit %d\n%s", code, stdout)
	}
	stdout, code = collectCLI(t, "codex", "/root/capture_target", "", filepath.Join(codex, "lookup/empty"))
	if code != 1 || !diagnosticCode(decodeReport(t, []byte(stdout)), "empty_source") {
		t.Fatalf("empty codex root: exit %d\n%s", code, stdout)
	}
	os.Unsetenv("CODEX_HOME")

	t.Setenv("OPENCODE_DB", filepath.Join(opencode, "lookup/configured.db"))
	stdout, code = collectCLI(t, "opencode-v2", "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", filepath.Join(opencode, "lookup/explicit.db"))
	if code != 0 || tokenValue(t, decodeReport(t, []byte(stdout)), "self", "input") != 10200 {
		t.Fatalf("explicit opencode database: exit %d\n%s", code, stdout)
	}
	stdout, code = collectCLI(t, "opencode-v2", "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", filepath.Join(opencode, "lookup/invalid.db"))
	if code != 1 || !diagnosticCode(decodeReport(t, []byte(stdout)), "malformed_record") {
		t.Fatalf("invalid opencode database: exit %d\n%s", code, stdout)
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	t.Setenv("OPENCODE_DB", missing)
	stdout, code = collectCLI(t, "opencode-v2", "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", "")
	if code != 1 || !diagnosticCode(decodeReport(t, []byte(stdout)), "invalid_source") || fileExists(missing) {
		t.Fatalf("missing OPENCODE_DB must not be created or replaced: exit %d exists %v\n%s", code, fileExists(missing), stdout)
	}
}

func TestCollectPrivacyAndReadOnly(t *testing.T) {
	root := moduleRoot(t)
	base := t.TempDir()
	prepare := func(script string) string {
		t.Helper()
		destination := filepath.Join(base, strings.ReplaceAll(script, "/", "_"))
		command := exec.Command("python3", filepath.Join(root, script), destination)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("prepare %s: %v\n%s", script, err, output)
		}
		return destination
	}
	claude := prepare("testdata/claude-code/native/prepare.py")
	codex := prepare("testdata/codex/native/prepare.py")
	opencode := prepare("testdata/opencode-v2/native/prepare.py")
	cases := []struct {
		product, agent, records string
	}{
		{"claude-code", "ada3d4768bc8b8ec8", filepath.Join(claude, "body-sentinel/projects")},
		{"codex", "/root/capture_target", filepath.Join(codex, "body-sentinel/sessions")},
		{"opencode-v2", "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", filepath.Join(opencode, "variants/body-sentinel.db")},
	}
	for _, tc := range cases {
		before := treeDigest(t, tc.records)
		var stdout, stderr bytes.Buffer
		code := run([]string{"agent-metrics", "collect", "--product", tc.product, "--agent", tc.agent, "--records", tc.records}, &stdout, &stderr)
		combined := stdout.String() + stderr.String()
		if containsText(combined, "FIXTURE_BODY_MUST_NOT_LEAK") {
			t.Fatalf("%s leaked the body sentinel", tc.product)
		}
		if code != 0 {
			t.Fatalf("%s exit %d\n%s", tc.product, code, stdout.String())
		}
		if treeDigest(t, tc.records) != before {
			t.Fatalf("%s records changed", tc.product)
		}
	}
	db := filepath.Join(opencode, "variants/body-sentinel.db")
	if fileExists(db+"-wal") || fileExists(db+"-shm") {
		t.Fatal("read-only SQLite created sidecar files")
	}
}

func clearSourceEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "OPENCODE_DB"} {
		t.Setenv(key, "unset")
		os.Unsetenv(key)
	}
}

func tokenValue(t *testing.T, decoded map[string]any, scope, name string) float64 {
	t.Helper()
	items := decoded["usage"].(map[string]any)[scope].([]any)
	var sum float64
	seen := false
	for _, item := range items {
		value := item.(map[string]any)["tokens"].(map[string]any)[name]
		if value == nil {
			continue
		}
		seen = true
		sum += value.(float64)
	}
	if !seen {
		return -1
	}
	return sum
}

func diagnosticCode(decoded map[string]any, code string) bool {
	diagnostics, _ := decoded["diagnostics"].([]any)
	for _, item := range diagnostics {
		if item.(map[string]any)["code"] == code {
			return true
		}
	}
	return false
}

func copyTree(t *testing.T, source, destination string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("cp", "-a", source, destination).CombinedOutput(); err != nil {
		t.Fatalf("copy %s: %v\n%s", source, err, output)
	}
}

func copyFileTo(t *testing.T, source, destination string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("cp", "-a", source, destination).CombinedOutput(); err != nil {
		t.Fatalf("copy %s: %v\n%s", source, err, output)
	}
}

func treeDigest(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		return string(sum[:])
	}
	sum := sha256.New()
	err = filepath.WalkDir(path, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		_, _ = sum.Write([]byte(name))
		_, _ = sum.Write(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(sum.Sum(nil))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func containsText(text, part string) bool {
	return len(text) >= len(part) && (text == part || len(part) > 0 && func() bool {
		for i := 0; i+len(part) <= len(text); i++ {
			if text[i:i+len(part)] == part {
				return true
			}
		}
		return false
	}())
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
	args := []string{"agent-metrics", "collect", "--product", product, "--agent", agent}
	if execution != "" {
		args = append(args, "--execution", execution)
	}
	if records != "" {
		args = append(args, "--records", records)
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
