package adapter_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/y-writings/agent-metrics/internal/adapter"
	"github.com/y-writings/agent-metrics/internal/report"
)

func TestClaudeR1ReturnedAgent(t *testing.T) {
	root := moduleRoot(t)
	got := measureClaude(t, "ada3d4768bc8b8ec8", "", filepath.Join(root, "testdata/claude-code/native/records/r1/projects"))
	if got.Completeness != "complete" || got.Status != "completed" || got.Target.ExecutionID == nil || *got.Target.ExecutionID != "db05adb4-2980-4b84-909b-d2481043a8ca" {
		t.Fatalf("target: %+v status %s completeness %s", got.Target, got.Status, got.Completeness)
	}
	assertTokens(t, got.Usage.Self, 4, 11533, 13718, 159, 0, 25414)
	assertTokens(t, got.Usage.Subtree, 4, 11533, 13718, 159, 0, 25414)
	if got.Timing.ElapsedMilliseconds == nil || *got.Timing.ElapsedMilliseconds != 5437 {
		t.Fatalf("elapsed: %+v", got.Timing)
	}
	if got.Usage.Self[0].Provider != nil || got.Usage.Self[0].Model == nil || *got.Usage.Self[0].Model != "claude-opus-5-5" || !hasCode(got.Diagnostics, "unknown_provider") {
		t.Fatalf("provider must stay unknown while the model and counters remain: %+v diagnostics %+v", got.Usage.Self, got.Diagnostics)
	}
}

func TestClaudeSingleTranscriptKeepsExecutionIdentity(t *testing.T) {
	root := moduleRoot(t)
	path := filepath.Join(root, "testdata/claude-code/native/records/r1/projects/-fixture-workspace-agent-metrics/a8d0d672-209c-4bab-86d6-48fec0f52850/subagents/agent-ada3d4768bc8b8ec8.jsonl")
	got := measureClaude(t, "ada3d4768bc8b8ec8", "", path)
	if got.Target.ExecutionID == nil || *got.Target.ExecutionID != "db05adb4-2980-4b84-909b-d2481043a8ca" || hasCode(got.Diagnostics, "missing_execution_id") {
		t.Fatalf("single transcript must keep the start user id: %+v diagnostics %+v", got.Target, got.Diagnostics)
	}
	assertTokens(t, got.Usage.Self, 4, 11533, 13718, 159, 0, 25414)
	if got.Timing.ElapsedMilliseconds != nil {
		t.Fatalf("parent stop notification is outside this file: %+v", got.Timing)
	}
	selected := measureClaude(t, "ada3d4768bc8b8ec8", "db05adb4-2980-4b84-909b-d2481043a8ca", path)
	if selected.Completeness == "incomplete" || selected.Target.ExecutionID == nil || *selected.Target.ExecutionID != "db05adb4-2980-4b84-909b-d2481043a8ca" {
		t.Fatalf("explicit execution must resolve inside the transcript: %+v", selected.Target)
	}
	assertTokens(t, selected.Usage.Self, 4, 11533, 13718, 159, 0, 25414)
}

func TestClaudeMultipleTurnsAndReuse(t *testing.T) {
	root := moduleRoot(t)
	records := filepath.Join(root, "testdata/claude-code/native/records/default/projects")
	ambiguous := measureClaude(t, "ada3d4768bc8b8ec8", "", records)
	if ambiguous.Completeness != "incomplete" || len(ambiguous.Diagnostics) != 1 || ambiguous.Diagnostics[0].Code != "ambiguous_execution" {
		t.Fatalf("reuse must be ambiguous: %+v", ambiguous.Diagnostics)
	}
	for _, id := range []string{
		"db05adb4-2980-4b84-909b-d2481043a8ca",
		"b3d9191c-6c78-4c2d-a207-cdb53f2ea0a8",
		"022e39c9-f7ff-425a-9e11-cb239b5d3d7c",
	} {
		if !contains(ambiguous.Diagnostics[0].Message, id) {
			t.Fatalf("selectable id %s missing from %s", id, ambiguous.Diagnostics[0].Message)
		}
	}
	got := measureClaude(t, "ada3d4768bc8b8ec8", "b3d9191c-6c78-4c2d-a207-cdb53f2ea0a8", records)
	assertTotal(t, got.Usage.Self, 60121)
	assertTotal(t, got.Usage.Subtree, 85659)
	if got.Timing.ElapsedMilliseconds == nil || *got.Timing.ElapsedMilliseconds != 14826 || *got.Timing.ElapsedMilliseconds == 5704 {
		t.Fatalf("T.B must not end at the intermediate turn: %+v", got.Timing)
	}
	if !executionTotal(got, 25538) || got.Completeness != "complete" || got.Usage.SelfCompleteness != "complete" || got.Usage.SubtreeCompleteness != "complete" {
		t.Fatalf("grandchild scope: %+v executions %+v", got.Usage, got.Executions)
	}
}

func TestClaudeMissingResponseUsageStaysPartial(t *testing.T) {
	root := moduleRoot(t)
	projects := filepath.Join(t.TempDir(), "projects")
	if output, err := exec.Command("cp", "-a", filepath.Join(root, "testdata/claude-code/native/records/r1/projects"), projects).CombinedOutput(); err != nil {
		t.Fatalf("copy projects: %v\n%s", err, output)
	}
	source := filepath.Join(projects, "-fixture-workspace-agent-metrics/a8d0d672-209c-4bab-86d6-48fec0f52850/subagents/agent-ada3d4768bc8b8ec8.jsonl")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	for _, line := range splitLines(raw) {
		if contains(string(line), "msg_011ChCcZwPD7f8yhCIKPXlfa") {
			var record map[string]any
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatal(err)
			}
			if message, ok := record["message"].(map[string]any); ok {
				delete(message, "usage")
			}
			rewritten, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			line = rewritten
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	if err := os.WriteFile(source, out, 0o600); err != nil {
		t.Fatal(err)
	}
	got := measureClaude(t, "ada3d4768bc8b8ec8", "", projects)
	assertTotal(t, got.Usage.Self, 13777)
	if got.Completeness != "partial" || got.Usage.SelfCompleteness != "partial" || got.Usage.SubtreeCompleteness != "partial" {
		t.Fatalf("a response without usage must not leave the remaining subtotal complete: %+v", got.Usage)
	}
}

func TestClaudeMissingMessageIDKeepsUsagePartial(t *testing.T) {
	root := moduleRoot(t)
	projects := filepath.Join(t.TempDir(), "projects")
	if output, err := exec.Command("cp", "-a", filepath.Join(root, "testdata/claude-code/native/records/r1/projects"), projects).CombinedOutput(); err != nil {
		t.Fatalf("copy projects: %v\n%s", err, output)
	}
	source := filepath.Join(projects, "-fixture-workspace-agent-metrics/a8d0d672-209c-4bab-86d6-48fec0f52850/subagents/agent-ada3d4768bc8b8ec8.jsonl")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	for _, line := range splitLines(raw) {
		if contains(string(line), "msg_011ChCcZwPD7f8yhCIKPXlfa") {
			var record map[string]any
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatal(err)
			}
			if message, ok := record["message"].(map[string]any); ok {
				delete(message, "id")
			}
			rewritten, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			line = rewritten
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	if err := os.WriteFile(source, out, 0o600); err != nil {
		t.Fatal(err)
	}
	got := measureClaude(t, "ada3d4768bc8b8ec8", "", projects)
	assertTotal(t, got.Usage.Self, 25414)
	assertTotal(t, got.Usage.Subtree, 25414)
	if got.Completeness != "partial" || got.Usage.SelfCompleteness != "partial" || got.Usage.SubtreeCompleteness != "partial" || !hasCode(got.Diagnostics, "missing_message_id") {
		t.Fatalf("usage without a message id must stay partial: %+v diagnostics %+v", got.Usage, got.Diagnostics)
	}
}

func TestClaudeUsageBeforeLaterInvocationStaysUnattributed(t *testing.T) {
	root := moduleRoot(t)
	projects := filepath.Join(t.TempDir(), "projects")
	if output, err := exec.Command("cp", "-a", filepath.Join(root, "testdata/claude-code/native/records/default/projects"), projects).CombinedOutput(); err != nil {
		t.Fatalf("copy projects: %v\n%s", err, output)
	}
	source := filepath.Join(projects, "-fixture-workspace-agent-metrics/a8d0d672-209c-4bab-86d6-48fec0f52850/subagents/agent-ada3d4768bc8b8ec8.jsonl")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	lines := splitLines(raw)
	if len(lines) < 2 {
		t.Fatal("agent transcript is too short")
	}
	var out []byte
	for _, line := range lines[1:] {
		out = append(out, line...)
		out = append(out, '\n')
	}
	if err := os.WriteFile(source, out, 0o600); err != nil {
		t.Fatal(err)
	}
	got := measureClaude(t, "ada3d4768bc8b8ec8", "b3d9191c-6c78-4c2d-a207-cdb53f2ea0a8", projects)
	assertTotal(t, got.Usage.Self, 60121)
	assertTotal(t, got.Usage.Subtree, 85659)
	var unattributed int64
	for _, usage := range got.UnattributedUsage {
		if usage.Tokens.Total != nil {
			unattributed += *usage.Tokens.Total
		}
	}
	if len(got.UnattributedUsage) != 2 || unattributed != 25414 || got.Completeness == "complete" || !hasCode(got.Diagnostics, "unscoped_usage") {
		t.Fatalf("usage before the remaining invocation must stay unattributed: self %+v unattributed %+v diagnostics %+v", got.Usage, got.UnattributedUsage, got.Diagnostics)
	}
	if got.Timing.ElapsedMilliseconds == nil || *got.Timing.ElapsedMilliseconds != 14826 {
		t.Fatalf("elapsed: %+v", got.Timing)
	}
}

func TestClaudeMissingMetaKeepsLaunchLinkedChild(t *testing.T) {
	root := moduleRoot(t)
	projects := filepath.Join(t.TempDir(), "projects")
	if output, err := exec.Command("cp", "-a", filepath.Join(root, "testdata/claude-code/native/records/default/projects"), projects).CombinedOutput(); err != nil {
		t.Fatalf("copy projects: %v\n%s", err, output)
	}
	meta := filepath.Join(projects, "-fixture-workspace-agent-metrics/a8d0d672-209c-4bab-86d6-48fec0f52850/subagents/agent-ac4c73eec0456b131.meta.json")
	if err := os.Remove(meta); err != nil {
		t.Fatal(err)
	}
	got := measureClaude(t, "ada3d4768bc8b8ec8", "b3d9191c-6c78-4c2d-a207-cdb53f2ea0a8", projects)
	assertTotal(t, got.Usage.Self, 60121)
	assertTotal(t, got.Usage.Subtree, 85659)
	if got.Completeness != "complete" || got.Usage.SubtreeCompleteness != "complete" || hasCode(got.Diagnostics, "missing_descendant") {
		t.Fatalf("a launch-linked child transcript must stay in the subtree without its meta sidecar: %+v diagnostics %+v", got.Usage, got.Diagnostics)
	}
	if got.Timing.ElapsedMilliseconds == nil || *got.Timing.ElapsedMilliseconds != 14826 {
		t.Fatalf("elapsed: %+v", got.Timing)
	}
}

func TestClaudeDuplicateLaunchReferenceStillLinksChild(t *testing.T) {
	root := moduleRoot(t)
	projects := filepath.Join(t.TempDir(), "projects")
	if output, err := exec.Command("cp", "-a", filepath.Join(root, "testdata/claude-code/native/records/default/projects"), projects).CombinedOutput(); err != nil {
		t.Fatalf("copy projects: %v\n%s", err, output)
	}
	transcript := filepath.Join(projects, "-fixture-workspace-agent-metrics/a8d0d672-209c-4bab-86d6-48fec0f52850/subagents/agent-ada3d4768bc8b8ec8.jsonl")
	raw, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	var repeated []byte
	var distinct []byte
	for _, line := range splitLines(raw) {
		repeated = append(repeated, line...)
		repeated = append(repeated, '\n')
		distinct = append(distinct, line...)
		distinct = append(distinct, '\n')
		if !contains(string(line), "ac4c73eec0456b131") || !contains(string(line), "async_launched") {
			continue
		}
		repeated = append(repeated, line...)
		repeated = append(repeated, '\n')
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		message := record["message"].(map[string]any)
		for _, item := range message["content"].([]any) {
			block := item.(map[string]any)
			if block["type"] == "tool_result" {
				block["tool_use_id"] = "toolu_distinct_unmatched_reference"
			}
		}
		rewritten, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		distinct = append(distinct, rewritten...)
		distinct = append(distinct, '\n')
	}
	if err := os.WriteFile(transcript, repeated, 0o600); err != nil {
		t.Fatal(err)
	}
	duplicated := measureClaude(t, "ada3d4768bc8b8ec8", "b3d9191c-6c78-4c2d-a207-cdb53f2ea0a8", projects)
	assertTotal(t, duplicated.Usage.Subtree, 85659)
	if duplicated.Completeness != "complete" || hasCode(duplicated.Diagnostics, "missing_descendant") {
		t.Fatalf("a repeated launch result must still link the grandchild: %+v", duplicated)
	}
	if err := os.WriteFile(transcript, distinct, 0o600); err != nil {
		t.Fatal(err)
	}
	unmatched := measureClaude(t, "ada3d4768bc8b8ec8", "b3d9191c-6c78-4c2d-a207-cdb53f2ea0a8", projects)
	assertTotal(t, unmatched.Usage.Self, 60121)
	assertTotal(t, unmatched.Usage.Subtree, 60121)
	if unmatched.Completeness != "partial" || unmatched.Usage.SubtreeCompleteness != "partial" || !hasCode(unmatched.Diagnostics, "missing_descendant") {
		t.Fatalf("an unmatched launch result must stay unresolved: %+v", unmatched)
	}
}

func TestClaudeHandbackAndInterrupt(t *testing.T) {
	root := moduleRoot(t)
	running := measureClaude(t, "a532a52d8e39263cd", "dfc3baeb-7b9f-4e35-89f3-a703ffb83524", filepath.Join(root, "testdata/claude-code/native/records/running-grandchild/projects"))
	if running.Timing.ElapsedMilliseconds != nil || running.Status == "completed" {
		t.Fatalf("handback is not the terminal boundary: status %s elapsed %+v", running.Status, running.Timing.ElapsedMilliseconds)
	}
	interrupted := measureClaude(t, "a532a52d8e39263cd", "cb2d67a0-4e3f-4697-8296-c447f10a7046", filepath.Join(root, "testdata/claude-code/native/records/default/projects"))
	assertTotal(t, interrupted.Usage.Self, 67110)
	if interrupted.Timing.ElapsedMilliseconds == nil || *interrupted.Timing.ElapsedMilliseconds != 9713 || interrupted.Status != "interrupted" || interrupted.Usage.SelfCompleteness != "partial" {
		t.Fatalf("interrupted execution: status %s elapsed %+v usage %+v", interrupted.Status, interrupted.Timing.ElapsedMilliseconds, interrupted.Usage)
	}
	grandchild := measureClaude(t, "a6db55dd0c04f1cce", "a9e2383c-0dad-4fec-99fa-55fc7a385be0", filepath.Join(root, "testdata/claude-code/native/records/default/projects"))
	if grandchild.Status != "completed" {
		t.Fatalf("a tool error must not fail the agent: %s", grandchild.Status)
	}
}

func TestClaudeNativeDerived(t *testing.T) {
	root := moduleRoot(t)
	derived := filepath.Join(t.TempDir(), "derived")
	command := exec.Command("python3", filepath.Join(root, "testdata/claude-code/native/prepare.py"), derived)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("prepare: %v\n%s", err, output)
	}
	before := fileDigest(t, filepath.Join(derived, "duplicate-row"))
	duplicate := measureClaude(t, "ada3d4768bc8b8ec8", "", filepath.Join(derived, "duplicate-row/projects"))
	assertTotal(t, duplicate.Usage.Self, 25414)
	if duplicate.Timing.ElapsedMilliseconds == nil || *duplicate.Timing.ElapsedMilliseconds != 5437 || duplicate.Completeness != "complete" {
		t.Fatalf("duplicate content row: %+v", duplicate)
	}
	if fileDigest(t, filepath.Join(derived, "duplicate-row")) != before {
		t.Fatal("reader changed derived records")
	}
	missingModel := measureClaude(t, "ada3d4768bc8b8ec8", "", filepath.Join(derived, "missing-model/projects"))
	assertTotal(t, missingModel.Usage.Self, 25414)
	if missingModel.Usage.Self[0].Model != nil || missingModel.Timing.ElapsedMilliseconds == nil || *missingModel.Timing.ElapsedMilliseconds != 5437 {
		t.Fatalf("missing model: %+v", missingModel.Usage.Self)
	}
	missingUsage := measureClaude(t, "ada3d4768bc8b8ec8", "", filepath.Join(derived, "missing-usage/projects"))
	if len(missingUsage.Usage.Self) != 0 || missingUsage.Timing.ElapsedMilliseconds == nil || *missingUsage.Timing.ElapsedMilliseconds != 5437 {
		t.Fatalf("notification tokens must not replace message usage: %+v", missingUsage)
	}
	missingNote := measureClaude(t, "ada3d4768bc8b8ec8", "", filepath.Join(derived, "missing-stop-notification/projects"))
	assertTotal(t, missingNote.Usage.Self, 25414)
	if missingNote.Timing.ElapsedMilliseconds != nil || missingNote.Status == "completed" {
		t.Fatalf("stop notification is required to close the execution: status %s elapsed %+v", missingNote.Status, missingNote.Timing.ElapsedMilliseconds)
	}
	truncated := measureClaude(t, "ada3d4768bc8b8ec8", "", filepath.Join(derived, "truncated-tail/projects"))
	if retainedClaude(truncated) != 25414 || truncated.Timing.ElapsedMilliseconds != nil || !hasCode(truncated.Diagnostics, "truncated_record") {
		t.Fatalf("truncated tail: %+v", truncated)
	}
	unknown := measureClaude(t, "ada3d4768bc8b8ec8", "", filepath.Join(derived, "unsupported-record/projects"))
	assertTotal(t, unknown.Usage.Self, 25414)
	if !hasCode(unknown.Diagnostics, "unsupported_record") || unknown.Completeness == "complete" {
		t.Fatalf("unsupported record: %+v", unknown.Diagnostics)
	}
	reversed := measureClaude(t, "ada3d4768bc8b8ec8", "", filepath.Join(derived, "reversed-time/projects"))
	assertTotal(t, reversed.Usage.Self, 25414)
	if reversed.Timing.ElapsedMilliseconds != nil || !hasCode(reversed.Diagnostics, "invalid_boundary") {
		t.Fatalf("reversed boundary: %+v", reversed.Timing)
	}
	prior := measureClaude(t, "ada3d4768bc8b8ec8", "", filepath.Join(derived, "removed-prior-invocation/projects"))
	if prior.Completeness != "incomplete" || !contains(prior.Diagnostics[0].Message, "b3d9191c-6c78-4c2d-a207-cdb53f2ea0a8") || contains(prior.Diagnostics[0].Message, "db05adb4-2980-4b84-909b-d2481043a8ca") {
		t.Fatalf("removed spawn must leave the later invocations selectable: %+v", prior.Diagnostics)
	}
	selected := measureClaude(t, "ada3d4768bc8b8ec8", "b3d9191c-6c78-4c2d-a207-cdb53f2ea0a8", filepath.Join(derived, "removed-prior-invocation/projects"))
	assertTotal(t, selected.Usage.Self, 60121)
	if selected.Timing.ElapsedMilliseconds == nil || *selected.Timing.ElapsedMilliseconds != 14826 || hasCode(selected.Diagnostics, "unknown_usage") {
		t.Fatalf("T.B must stay independent of the removed invocation and of S.C: %+v", selected)
	}
	sentinel := measureClaude(t, "ada3d4768bc8b8ec8", "", filepath.Join(derived, "body-sentinel/projects"))
	encoded, _ := json.Marshal(sentinel)
	if contains(string(encoded), "FIXTURE_BODY_MUST_NOT_LEAK") {
		t.Fatal("report contains the body sentinel")
	}
	conflict := conflictingClaude(t, filepath.Join(root, "testdata/claude-code/native/records/r1/projects"))
	conflicting := measureClaude(t, "ada3d4768bc8b8ec8", "", conflict)
	if !hasCode(conflicting.Diagnostics, "duplicate_conflict") || conflicting.Completeness != "partial" || retainedClaude(conflicting) >= 25414 {
		t.Fatalf("conflicting message usage must not be chosen: %+v", conflicting)
	}
}

func measureClaude(t *testing.T, agent, execution, records string) report.ExecutionReport {
	t.Helper()
	reader, err := adapter.New("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.Read(adapter.Request{Agent: agent, Execution: execution, Records: records})
	if err != nil {
		t.Fatal(err)
	}
	return report.Collect("claude-code", adapter.Request{Agent: agent, Execution: execution}, result)
}

func executionTotal(got report.ExecutionReport, total int64) bool {
	for _, execution := range got.Executions {
		if sumCounter(execution.Usage, func(tokens report.Tokens) *int64 { return tokens.Total }) == total {
			return true
		}
	}
	return false
}

func retainedClaude(got report.ExecutionReport) int64 {
	total := sumCounter(got.Usage.Self, func(tokens report.Tokens) *int64 { return tokens.Total })
	if total < 0 {
		total = 0
	}
	for _, usage := range got.UnattributedUsage {
		if usage.Tokens.Total != nil {
			total += *usage.Tokens.Total
		}
	}
	return total
}

func conflictingClaude(t *testing.T, projects string) string {
	t.Helper()
	source := filepath.Join(projects, "-fixture-workspace-agent-metrics/a8d0d672-209c-4bab-86d6-48fec0f52850/subagents/agent-ada3d4768bc8b8ec8.jsonl")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	changed := false
	for _, line := range splitLines(raw) {
		out = append(out, line...)
		out = append(out, '\n')
		if changed || !contains(string(line), "\"type\":\"assistant\"") && !contains(string(line), "\"type\": \"assistant\"") {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		message, _ := record["message"].(map[string]any)
		usage, _ := message["usage"].(map[string]any)
		if usage == nil {
			continue
		}
		usage["output_tokens"] = 1
		rewritten, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, rewritten...)
		out = append(out, '\n')
		changed = true
	}
	dir := t.TempDir()
	destination := filepath.Join(dir, "projects/-fixture/subagents")
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "agent-ada3d4768bc8b8ec8.jsonl"), out, 0o600); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(filepath.Dir(source), "agent-ada3d4768bc8b8ec8.meta.json")
	copied, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "agent-ada3d4768bc8b8ec8.meta.json"), copied, 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "projects")
}
