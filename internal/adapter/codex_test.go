package adapter_test

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/y-writings/agent-metrics/internal/adapter"
	"github.com/y-writings/agent-metrics/internal/report"
)

func TestCodexR1ReturnedPath(t *testing.T) {
	root := moduleRoot(t)
	got := measureCodex(t, "/root/capture_target", "", filepath.Join(root, "testdata/codex/native/records/r1/sessions"))
	if got.Completeness != "complete" || got.Status != "completed" || got.Target.AgentID != "/root/capture_target" || got.Target.ExecutionID == nil {
		t.Fatalf("target: %+v status %s completeness %s", got.Target, got.Status, got.Completeness)
	}
	assertTokens(t, got.Usage.Self, 40004, 26752, 0, 76, 0, 40080)
	assertTokens(t, got.Usage.Subtree, 40004, 26752, 0, 76, 0, 40080)
	if got.Timing.ElapsedMilliseconds == nil || *got.Timing.ElapsedMilliseconds != 8994 {
		t.Fatalf("elapsed: %+v", got.Timing)
	}
	if got.Usage.Self[0].Provider == nil || *got.Usage.Self[0].Provider != "openai" || got.Usage.Self[0].Model == nil || *got.Usage.Self[0].Model != "gpt-6-astra" {
		t.Fatalf("attribution: %+v", got.Usage.Self)
	}
	if got.Usage.SelfCompleteness != "complete" || got.Usage.SubtreeCompleteness != "complete" {
		t.Fatalf("usage scopes: %+v", got.Usage)
	}
}

func TestCodexReuseAndDescendants(t *testing.T) {
	root := moduleRoot(t)
	records := filepath.Join(root, "testdata/codex/native/records/default/sessions")
	ambiguous := measureCodex(t, "/root/capture_target", "", records)
	if ambiguous.Completeness != "incomplete" || len(ambiguous.Diagnostics) != 1 || ambiguous.Diagnostics[0].Code != "ambiguous_execution" {
		t.Fatalf("reuse without an execution must be ambiguous: %+v", ambiguous.Diagnostics)
	}
	for _, id := range []string{
		"9e7527c6-6f74-7c6a-be67-5ada92b1497b",
		"b16afe9d-0ec9-7c62-80d5-b12717dbd878",
		"598c2c4a-92f5-78ea-8b13-6e4ed9891f5c",
	} {
		if !contains(ambiguous.Diagnostics[0].Message, id) {
			t.Fatalf("selectable executions missing %s: %s", id, ambiguous.Diagnostics[0].Message)
		}
	}
	got := measureCodex(t, "/root/capture_target", "b16afe9d-0ec9-7c62-80d5-b12717dbd878", records)
	assertTokens(t, got.Usage.Self, 82038, 81024, 0, 381, 123, 82419)
	assertTokens(t, got.Usage.Subtree, 122200, 120704, 0, 582, 209, 122782)
	if got.Timing.ElapsedMilliseconds == nil || *got.Timing.ElapsedMilliseconds != 43699 || got.Status != "completed" || got.Usage.SelfCompleteness != "complete" || got.Usage.SubtreeCompleteness != "complete" {
		t.Fatalf("T.B measurement: status %s elapsed %+v usage %+v", got.Status, got.Timing.ElapsedMilliseconds, got.Usage)
	}
}

func TestCodexReferenceCollision(t *testing.T) {
	dir := t.TempDir()
	writeRollout(t, filepath.Join(dir, "older.jsonl"), "thread-older", "/root/same", "turn-older", 2, "2026-01-01T00:00:00.000Z")
	writeRollout(t, filepath.Join(dir, "newer.jsonl"), "thread-newer", "/root/same", "turn-newer", 9, "2026-01-02T00:00:00.000Z")
	result, err := adapter.New("codex")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := result.Read(adapter.Request{Agent: "/root/same", Records: dir})
	if err != nil {
		t.Fatal(err)
	}
	got := report.Collect("codex", adapter.Request{Agent: "/root/same"}, parsed)
	if got.Completeness != "incomplete" || got.Target.ExecutionID != nil || len(got.Usage.Self) != 0 {
		t.Fatalf("collision must not select a thread: %+v", got)
	}
	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "ambiguous_reference" {
		t.Fatalf("diagnostics: %+v", got.Diagnostics)
	}
}

func TestCodexPartialAndDerived(t *testing.T) {
	root := moduleRoot(t)
	records := filepath.Join(root, "testdata/codex/native/records/default/sessions")
	running := measureCodex(t, "/root/capture_target", "598c2c4a-92f5-78ea-8b13-6e4ed9891f5c", records)
	assertTotal(t, running.Usage.Self, 108182)
	assertTotal(t, running.Usage.Subtree, 169109)
	if running.Timing.ElapsedMilliseconds == nil || *running.Timing.ElapsedMilliseconds != 31270 || running.Usage.SelfCompleteness != "complete" || running.Usage.SubtreeCompleteness != "partial" || running.Completeness != "partial" {
		t.Fatalf("running grandchild must stay a partial subtree: elapsed %+v usage %+v completeness %s", running.Timing.ElapsedMilliseconds, running.Usage, running.Completeness)
	}
	derived := filepath.Join(t.TempDir(), "derived")
	command := exec.Command("python3", filepath.Join(root, "testdata/codex/native/prepare.py"), derived)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("prepare: %v\n%s", err, output)
	}
	before := fileDigest(t, filepath.Join(derived, "missing-baseline/sessions"))
	baseline := measureCodex(t, "/root/capture_target", "b16afe9d-0ec9-7c62-80d5-b12717dbd878", filepath.Join(derived, "missing-baseline/sessions"))
	assertTotal(t, baseline.Usage.Self, 82419)
	if baseline.Timing.ElapsedMilliseconds == nil || *baseline.Timing.ElapsedMilliseconds != 43699 {
		t.Fatalf("baseline-independent deltas: %+v", baseline.Timing)
	}
	if fileDigest(t, filepath.Join(derived, "missing-baseline/sessions")) != before {
		t.Fatal("reader changed derived records")
	}
	duplicate := measureCodex(t, "/root/capture_target", "", filepath.Join(derived, "duplicate-response/sessions"))
	assertTotal(t, duplicate.Usage.Self, 40080)
	if duplicate.Timing.ElapsedMilliseconds == nil || *duplicate.Timing.ElapsedMilliseconds != 8994 || duplicate.Completeness != "complete" {
		t.Fatalf("matching duplicate response: %+v", duplicate)
	}
	missingModel := measureCodex(t, "/root/capture_target", "", filepath.Join(derived, "missing-model/sessions"))
	assertTotal(t, missingModel.Usage.Self, 40080)
	if missingModel.Usage.Self[0].Model != nil || missingModel.Timing.ElapsedMilliseconds == nil || *missingModel.Timing.ElapsedMilliseconds != 8994 || missingModel.Usage.SelfCompleteness != "partial" {
		t.Fatalf("missing model: %+v elapsed %+v", missingModel.Usage.Self, missingModel.Timing.ElapsedMilliseconds)
	}
	missingUsage := measureCodex(t, "/root/capture_target", "", filepath.Join(derived, "missing-usage/sessions"))
	if len(missingUsage.Usage.Self) != 0 || missingUsage.Timing.ElapsedMilliseconds == nil || *missingUsage.Timing.ElapsedMilliseconds != 8994 || missingUsage.Usage.SelfCompleteness != "incomplete" {
		t.Fatalf("missing usage must not become zero: %+v", missingUsage)
	}
	truncated := measureCodex(t, "/root/capture_target", "", filepath.Join(derived, "truncated-tail/sessions"))
	assertTotal(t, truncated.Usage.Self, 40080)
	if truncated.Timing.ElapsedMilliseconds != nil || !hasCode(truncated.Diagnostics, "truncated_record") {
		t.Fatalf("truncated tail: elapsed %+v diagnostics %+v", truncated.Timing.ElapsedMilliseconds, truncated.Diagnostics)
	}
	unknown := measureCodex(t, "/root/capture_target", "", filepath.Join(derived, "unsupported-record/sessions"))
	assertTotal(t, unknown.Usage.Self, 40080)
	if !hasCode(unknown.Diagnostics, "unsupported_record") || unknown.Completeness == "complete" {
		t.Fatalf("unsupported record: %+v", unknown.Diagnostics)
	}
	reversed := measureCodex(t, "/root/capture_target", "", filepath.Join(derived, "reversed-time/sessions"))
	assertTotal(t, reversed.Usage.Self, 40080)
	if reversed.Timing.ElapsedMilliseconds != nil || !hasCode(reversed.Diagnostics, "invalid_boundary") {
		t.Fatalf("reversed boundary: %+v", reversed)
	}
	conflict := conflictingUsage(t, filepath.Join(root, "testdata/codex/native/records/r1/sessions"))
	conflicting := measureCodex(t, "/root/capture_target", "", conflict)
	if !hasCode(conflicting.Diagnostics, "duplicate_conflict") || conflicting.Completeness != "partial" || sumCounter(conflicting.Usage.Self, func(tokens report.Tokens) *int64 { return tokens.Total }) >= 40080 {
		t.Fatalf("conflicting duplicate must not be chosen or doubled: %+v", conflicting)
	}
	sentinel := measureCodex(t, "/root/capture_target", "", filepath.Join(derived, "body-sentinel/sessions"))
	encoded, _ := json.Marshal(sentinel)
	if contains(string(encoded), "FIXTURE_BODY_MUST_NOT_LEAK") {
		t.Fatal("report contains the body sentinel")
	}
}

func measureCodex(t *testing.T, agent, execution, records string) report.ExecutionReport {
	t.Helper()
	reader, err := adapter.New("codex")
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.Read(adapter.Request{Agent: agent, Execution: execution, Records: records})
	if err != nil {
		t.Fatal(err)
	}
	return report.Collect("codex", adapter.Request{Agent: agent, Execution: execution}, result)
}

func assertTokens(t *testing.T, usage []report.ModelUsage, input, cacheRead, cacheWrite, output, reasoning, total int64) {
	t.Helper()
	if sumCounter(usage, func(tokens report.Tokens) *int64 { return tokens.Input }) != input ||
		sumCounter(usage, func(tokens report.Tokens) *int64 { return tokens.CacheRead }) != cacheRead ||
		sumCounter(usage, func(tokens report.Tokens) *int64 { return tokens.CacheWrite }) != cacheWrite ||
		sumCounter(usage, func(tokens report.Tokens) *int64 { return tokens.Output }) != output ||
		sumCounter(usage, func(tokens report.Tokens) *int64 { return tokens.Reasoning }) != reasoning ||
		sumCounter(usage, func(tokens report.Tokens) *int64 { return tokens.Total }) != total {
		t.Fatalf("tokens got %+v", usage)
	}
}

func assertTotal(t *testing.T, usage []report.ModelUsage, total int64) {
	t.Helper()
	if sumCounter(usage, func(tokens report.Tokens) *int64 { return tokens.Total }) != total {
		t.Fatalf("total got %+v want %d", usage, total)
	}
}

func sumCounter(usage []report.ModelUsage, get func(report.Tokens) *int64) int64 {
	var sum int64
	seen := false
	for _, item := range usage {
		value := get(item.Tokens)
		if value == nil {
			continue
		}
		seen = true
		sum += *value
	}
	if !seen {
		return -1
	}
	return sum
}

func writeRollout(t *testing.T, path, thread, agent, turn string, total int64, stamp string) {
	t.Helper()
	input := total - 1
	lines := []any{
		map[string]any{"timestamp": stamp, "type": "session_meta", "payload": map[string]any{"id": thread, "session_id": "shared-root", "agent_path": agent, "model_provider": "openai"}},
		map[string]any{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": turn, "turn_attribution": map[string]any{"turn_id": turn, "parent_turn_id": "parent-turn"}}},
		map[string]any{"timestamp": stamp, "type": "turn_context", "payload": map[string]any{"turn_id": turn, "model": "gpt-6-astra"}},
		map[string]any{"timestamp": stamp, "type": "token_usage_record", "payload": map[string]any{"thread_id": thread, "turn_id": turn, "session_id": "shared-root", "response_id": "resp-" + thread, "usage": map[string]any{"input_tokens": input, "cached_input_tokens": 0, "cache_write_input_tokens": 0, "output_tokens": 1, "reasoning_output_tokens": 0, "total_tokens": total}, "thread_token_usage": map[string]any{"total_tokens": 999999}}},
		map[string]any{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{"type": "task_complete", "turn_id": turn}},
	}
	var encoded []byte
	for _, line := range lines {
		raw, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		encoded = append(encoded, raw...)
		encoded = append(encoded, '\n')
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func conflictingUsage(t *testing.T, sessions string) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(sessions, "2026/01/01/rollout-2026-01-01T20-33-36-8f89828d-ffe7-73df-8c40-fc37fa99eb91.jsonl")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	changed := false
	for _, line := range splitLines(raw) {
		out = append(out, line...)
		out = append(out, '\n')
		if !changed && contains(string(line), "token_usage_record") {
			var record map[string]any
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatal(err)
			}
			payload := record["payload"].(map[string]any)
			usage := payload["usage"].(map[string]any)
			usage["total_tokens"] = 1
			rewritten, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, rewritten...)
			out = append(out, '\n')
			changed = true
		}
	}
	destination := filepath.Join(dir, "conflict.jsonl")
	if err := os.WriteFile(destination, out, 0o600); err != nil {
		t.Fatal(err)
	}
	return destination
}

func splitLines(raw []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range raw {
		if b == '\n' {
			if i > start {
				lines = append(lines, raw[start:i])
			}
			start = i + 1
		}
	}
	if start < len(raw) {
		lines = append(lines, raw[start:])
	}
	return lines
}

func fileDigest(t *testing.T, root string) string {
	t.Helper()
	sum := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, _ = sum.Write([]byte(path))
		_, _ = sum.Write(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(sum.Sum(nil))
}

func hasCode(diagnostics []report.Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func contains(text, part string) bool {
	return len(part) == 0 || (len(text) >= len(part) && (text == part || len(text) > 0 && (func() bool {
		for i := 0; i+len(part) <= len(text); i++ {
			if text[i:i+len(part)] == part {
				return true
			}
		}
		return false
	})()))
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
