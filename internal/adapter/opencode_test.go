package adapter_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/y-writings/agent-metrics/internal/adapter"
	"github.com/y-writings/agent-metrics/internal/report"
	_ "modernc.org/sqlite"
)

func TestOpenCodeR1AndReuse(t *testing.T) {
	root := moduleRoot(t)
	r1 := filepath.Join(root, "testdata/opencode-v2/native/records/r1.db")
	before := sha256File(t, r1)
	got := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", r1)
	assertOpenCode(t, got.Usage.Self, 10200, 66, 211, 24704, 0)
	if got.Timing.ElapsedMilliseconds == nil || *got.Timing.ElapsedMilliseconds != 10099 || got.Status != "succeeded" || got.Completeness != "complete" || got.Usage.Self[0].Tokens.Total != nil {
		t.Fatalf("T.A: status %s elapsed %+v usage %+v completeness %s", got.Status, got.Timing.ElapsedMilliseconds, got.Usage.Self, got.Completeness)
	}
	if sha256File(t, r1) != before || fileExists(r1+"-wal") || fileExists(r1+"-shm") {
		t.Fatal("reading r1 changed the database or created a sidecar")
	}
	reuse := filepath.Join(root, "testdata/opencode-v2/native/records/default.db")
	ambiguous := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", reuse)
	if ambiguous.Completeness != "incomplete" || ambiguous.Diagnostics[0].Code != "ambiguous_execution" || !contains(ambiguous.Diagnostics[0].Message, "msg_D1vhyAAnkb61jlssqYaSAVIr3e") {
		t.Fatalf("reused session must be ambiguous: %+v", ambiguous.Diagnostics)
	}
	selected := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "msg_D1vhyAAnkb61jlssqYaSAVIr3e", reuse)
	assertOpenCode(t, selected.Usage.Self, 303, 49, 87, 24448, 0)
	if selected.Timing.ElapsedMilliseconds == nil || *selected.Timing.ElapsedMilliseconds != 7429 || sumCounter(selected.Usage.Self, func(tokens report.Tokens) *int64 { return tokens.Input }) == 10503 {
		t.Fatalf("T.B must not use the session lifetime cumulative: %+v", selected.Usage.Self)
	}
}

func TestOpenCodeMissingResponseUsageStaysPartial(t *testing.T) {
	root := moduleRoot(t)
	path := filepath.Join(t.TempDir(), "missing-response.db")
	copyFile(t, filepath.Join(root, "testdata/opencode-v2/native/records/r1.db"), path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var data string
	if err := db.QueryRow(`SELECT data FROM session_message WHERE id = ?`, "msg_2ou44EfXGCu7cZCEaovIsRGV3h").Scan(&data); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		t.Fatal(err)
	}
	delete(payload, "tokens")
	rewritten, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE session_message SET data = ? WHERE id = ?`, string(rewritten), "msg_2ou44EfXGCu7cZCEaovIsRGV3h"); err != nil {
		t.Fatal(err)
	}
	got := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", path)
	assertOpenCode(t, got.Usage.Self, 1559, 45, 49, 22656, 0)
	if got.Completeness != "partial" || got.Usage.SelfCompleteness != "partial" || got.Usage.SubtreeCompleteness != "partial" {
		t.Fatalf("a response without tokens must not leave the remaining subtotal complete: %+v", got.Usage)
	}
}

func TestOpenCodeMissingResponseRowStaysPartial(t *testing.T) {
	root := moduleRoot(t)
	path := filepath.Join(t.TempDir(), "missing-response-row.db")
	copyFile(t, filepath.Join(root, "testdata/opencode-v2/native/records/r1.db"), path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM session_message WHERE id = ?`, "msg_2ou44EfXGCu7cZCEaovIsRGV3h"); err != nil {
		t.Fatal(err)
	}
	got := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", path)
	assertOpenCode(t, got.Usage.Self, 1559, 45, 49, 22656, 0)
	if got.Completeness != "partial" || got.Usage.SelfCompleteness != "partial" || got.Usage.SubtreeCompleteness != "partial" || !hasCode(got.Diagnostics, "session_usage_mismatch") {
		t.Fatalf("missing response row must not leave a fresh-session subtotal complete: %+v diagnostics %+v", got.Usage, got.Diagnostics)
	}
	if got.Timing.ElapsedMilliseconds == nil || *got.Timing.ElapsedMilliseconds != 10099 {
		t.Fatalf("missing usage row must not change execution timing: %+v", got.Timing)
	}
}

func TestOpenCodeMissingStartKeepsUsage(t *testing.T) {
	root := moduleRoot(t)
	path := filepath.Join(t.TempDir(), "missing-start.db")
	copyFile(t, filepath.Join(root, "testdata/opencode-v2/native/records/r1.db"), path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM session_message WHERE id = ?`, "msg_qn9Nm9lAM88DnBsu9IEghMHl4B"); err != nil {
		t.Fatal(err)
	}
	got := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", path)
	var unattributed report.Tokens
	for _, usage := range got.UnattributedUsage {
		addOpenCode(&unattributed, usage.Tokens)
	}
	if got.Target.ExecutionID != nil || got.Timing.ElapsedMilliseconds != nil || hasCode(got.Diagnostics, "execution_not_found") {
		t.Fatalf("missing start must not invent an execution: %+v elapsed %+v", got.Target, got.Timing.ElapsedMilliseconds)
	}
	if value(unattributed.Input) != 10200 || value(unattributed.Output) != 66 || value(unattributed.Reasoning) != 211 || value(unattributed.CacheRead) != 24704 || value(unattributed.CacheWrite) != 0 || unattributed.Total != nil {
		t.Fatalf("observed counters must stay unattributed: %+v", unattributed)
	}
}

func addOpenCode(sum *report.Tokens, extra report.Tokens) {
	add := func(dst **int64, value *int64) {
		if value == nil {
			return
		}
		if *dst == nil {
			zero := int64(0)
			*dst = &zero
		}
		**dst += *value
	}
	add(&sum.Input, extra.Input)
	add(&sum.Output, extra.Output)
	add(&sum.Reasoning, extra.Reasoning)
	add(&sum.CacheRead, extra.CacheRead)
	add(&sum.CacheWrite, extra.CacheWrite)
	add(&sum.Total, extra.Total)
}

func value(n *int64) int64 {
	if n == nil {
		return -1
	}
	return *n
}

func TestOpenCodeReusedChildKeepsSeparateParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reuse.db")
	writeOpenCodeDB(t, path, []string{
		`CREATE TABLE session_v2 (id TEXT PRIMARY KEY, parent_id TEXT, model TEXT, time_created INTEGER, tokens_input INTEGER, tokens_output INTEGER, tokens_reasoning INTEGER, tokens_cache_read INTEGER, tokens_cache_write INTEGER)`,
		`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session_v2 VALUES ('ses_parent', NULL, '{"id":"m","providerID":"openai"}', 1000, NULL, NULL, NULL, NULL, NULL)`,
		`INSERT INTO session_v2 VALUES ('ses_child', 'ses_parent', '{"id":"m","providerID":"openai"}', 1100, NULL, NULL, NULL, NULL, NULL)`,
		`INSERT INTO session_message VALUES ('msg_p1', 'ses_parent', 'user', 1, 1000, 1000, '{"time":{"created":1000}}')`,
		`INSERT INTO session_message VALUES ('msg_p1a', 'ses_parent', 'assistant', 2, 1100, 1100, '{"time":{"created":1100},"model":{"id":"m","providerID":"openai"},"native":{"responseId":"resp-p1"},"tokens":{"input":10,"output":1,"reasoning":0,"cache":{"read":0,"write":0}},"content":[{"type":"tool","name":"subagent","state":{"status":"completed","metadata":{"sessionID":"ses_child"}}}]}')`,
		`INSERT INTO session_message VALUES ('msg_p1i', 'ses_parent', 'idle', 3, 2000, 2000, '{"time":{"created":2000},"outcome":"succeeded"}')`,
		`INSERT INTO session_message VALUES ('msg_p2', 'ses_parent', 'user', 4, 3000, 3000, '{"time":{"created":3000}}')`,
		`INSERT INTO session_message VALUES ('msg_p2a', 'ses_parent', 'assistant', 5, 3100, 3100, '{"time":{"created":3100},"model":{"id":"m","providerID":"openai"},"native":{"responseId":"resp-p2"},"tokens":{"input":20,"output":1,"reasoning":0,"cache":{"read":0,"write":0}},"content":[{"type":"tool","name":"subagent","state":{"status":"completed","input":{"sessionID":"ses_child"}}}]}')`,
		`INSERT INTO session_message VALUES ('msg_p2i', 'ses_parent', 'idle', 6, 4000, 4000, '{"time":{"created":4000},"outcome":"succeeded"}')`,
		`INSERT INTO session_message VALUES ('msg_c1', 'ses_child', 'user', 1, 1200, 1200, '{"time":{"created":1200}}')`,
		`INSERT INTO session_message VALUES ('msg_c1a', 'ses_child', 'assistant', 2, 1300, 1300, '{"time":{"created":1300},"model":{"id":"m","providerID":"openai"},"native":{"responseId":"resp-c1"},"tokens":{"input":1,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}}')`,
		`INSERT INTO session_message VALUES ('msg_c1i', 'ses_child', 'idle', 3, 1400, 1400, '{"time":{"created":1400},"outcome":"succeeded"}')`,
		`INSERT INTO session_message VALUES ('msg_c2', 'ses_child', 'user', 4, 3200, 3200, '{"time":{"created":3200}}')`,
		`INSERT INTO session_message VALUES ('msg_c2a', 'ses_child', 'assistant', 5, 3300, 3300, '{"time":{"created":3300},"model":{"id":"m","providerID":"openai"},"native":{"responseId":"resp-c2"},"tokens":{"input":2,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}}')`,
		`INSERT INTO session_message VALUES ('msg_c2i', 'ses_child', 'idle', 6, 3400, 3400, '{"time":{"created":3400},"outcome":"succeeded"}')`,
	})
	first := measureOpenCode(t, "ses_parent", "msg_p1", path)
	assertOpenCode(t, first.Usage.Self, 10, 1, 0, 0, 0)
	assertOpenCode(t, first.Usage.Subtree, 11, 2, 0, 0, 0)
	second := measureOpenCode(t, "ses_parent", "msg_p2", path)
	assertOpenCode(t, second.Usage.Self, 20, 1, 0, 0, 0)
	assertOpenCode(t, second.Usage.Subtree, 22, 2, 0, 0, 0)
}

func TestOpenCodeUnmatchedChildReferenceStaysPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unmatched.db")
	writeOpenCodeDB(t, path, []string{
		`CREATE TABLE session_v2 (id TEXT PRIMARY KEY, parent_id TEXT, model TEXT, time_created INTEGER, tokens_input INTEGER, tokens_output INTEGER, tokens_reasoning INTEGER, tokens_cache_read INTEGER, tokens_cache_write INTEGER)`,
		`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session_v2 VALUES ('ses_parent', NULL, '{"id":"m","providerID":"openai"}', 1000, NULL, NULL, NULL, NULL, NULL)`,
		`INSERT INTO session_v2 VALUES ('ses_child', 'ses_parent', '{"id":"m","providerID":"openai"}', 1100, NULL, NULL, NULL, NULL, NULL)`,
		`INSERT INTO session_message VALUES ('msg_p1', 'ses_parent', 'user', 1, 1000, 1000, '{"time":{"created":1000}}')`,
		`INSERT INTO session_message VALUES ('msg_p1a', 'ses_parent', 'assistant', 2, 1100, 1100, '{"time":{"created":1100},"model":{"id":"m","providerID":"openai"},"native":{"responseId":"resp-p1"},"tokens":{"input":10,"output":1,"reasoning":0,"cache":{"read":0,"write":0}},"content":[{"type":"tool","name":"subagent","state":{"status":"completed","metadata":{"sessionID":"ses_child"}}}]}')`,
		`INSERT INTO session_message VALUES ('msg_p1i', 'ses_parent', 'idle', 3, 2000, 2000, '{"time":{"created":2000},"outcome":"succeeded"}')`,
		`INSERT INTO session_message VALUES ('msg_p2', 'ses_parent', 'user', 4, 3000, 3000, '{"time":{"created":3000}}')`,
		`INSERT INTO session_message VALUES ('msg_p2a', 'ses_parent', 'assistant', 5, 3100, 3100, '{"time":{"created":3100},"model":{"id":"m","providerID":"openai"},"native":{"responseId":"resp-p2"},"tokens":{"input":20,"output":1,"reasoning":0,"cache":{"read":0,"write":0}},"content":[{"type":"tool","name":"subagent","state":{"status":"completed","input":{"sessionID":"ses_child"}}}]}')`,
		`INSERT INTO session_message VALUES ('msg_p2i', 'ses_parent', 'idle', 6, 4000, 4000, '{"time":{"created":4000},"outcome":"succeeded"}')`,
		`INSERT INTO session_message VALUES ('msg_c1', 'ses_child', 'user', 1, 1200, 1200, '{"time":{"created":1200}}')`,
		`INSERT INTO session_message VALUES ('msg_c1a', 'ses_child', 'assistant', 2, 1300, 1300, '{"time":{"created":1300},"model":{"id":"m","providerID":"openai"},"native":{"responseId":"resp-c1"},"tokens":{"input":1,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}}')`,
		`INSERT INTO session_message VALUES ('msg_c1i', 'ses_child', 'idle', 3, 1400, 1400, '{"time":{"created":1400},"outcome":"succeeded"}')`,
	})
	for _, execution := range []struct {
		id    string
		input int64
	}{
		{"msg_p1", 10},
		{"msg_p2", 20},
	} {
		got := measureOpenCode(t, "ses_parent", execution.id, path)
		assertOpenCode(t, got.Usage.Self, execution.input, 1, 0, 0, 0)
		assertOpenCode(t, got.Usage.Subtree, execution.input, 1, 0, 0, 0)
		if got.Completeness != "partial" || got.Usage.SubtreeCompleteness != "partial" || !hasCode(got.Diagnostics, "missing_descendant") {
			t.Fatalf("%s must not treat an unmatched child reference as childless: %+v", execution.id, got)
		}
	}
}

func TestOpenCodeChildSessionWithoutMessagesStaysPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty-child.db")
	writeOpenCodeDB(t, path, []string{
		`CREATE TABLE session_v2 (id TEXT PRIMARY KEY, parent_id TEXT, model TEXT, time_created INTEGER, tokens_input INTEGER, tokens_output INTEGER, tokens_reasoning INTEGER, tokens_cache_read INTEGER, tokens_cache_write INTEGER)`,
		`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session_v2 VALUES ('ses_parent', NULL, '{"id":"m","providerID":"openai"}', 1000, NULL, NULL, NULL, NULL, NULL)`,
		`INSERT INTO session_v2 VALUES ('ses_empty', 'ses_parent', '{"id":"m","providerID":"openai"}', 1100, NULL, NULL, NULL, NULL, NULL)`,
		`INSERT INTO session_message VALUES ('msg_p', 'ses_parent', 'user', 1, 1000, 1000, '{"time":{"created":1000}}')`,
		`INSERT INTO session_message VALUES ('msg_a', 'ses_parent', 'assistant', 2, 1100, 1100, '{"time":{"created":1100},"model":{"id":"m","providerID":"openai"},"native":{"responseId":"resp-p"},"tokens":{"input":7,"output":1,"reasoning":0,"cache":{"read":0,"write":0}},"content":[{"type":"tool","name":"subagent","state":{"status":"completed","metadata":{"sessionID":"ses_empty"}}}]}')`,
		`INSERT INTO session_message VALUES ('msg_i', 'ses_parent', 'idle', 3, 2000, 2000, '{"time":{"created":2000},"outcome":"succeeded"}')`,
	})
	got := measureOpenCode(t, "ses_parent", "", path)
	assertOpenCode(t, got.Usage.Self, 7, 1, 0, 0, 0)
	if got.Usage.SubtreeCompleteness != "partial" || got.Completeness != "partial" || !hasCode(got.Diagnostics, "missing_descendant") {
		t.Fatalf("session row without messages must not look childless: %+v", got)
	}
}

func TestOpenCodeDelegationAndState(t *testing.T) {
	root := moduleRoot(t)
	records := filepath.Join(root, "testdata/opencode-v2/native/records")
	got := measureOpenCode(t, "ses_czZw9XRushgm6LMu8axlUsfVy6", "", filepath.Join(records, "override.db"))
	assertOpenCode(t, got.Usage.Subtree, 13235, 188, 311, 23936, 0)
	assertOpenCode(t, got.Usage.Self, 1544, 131, 204, 2560, 0)
	if got.Timing.ElapsedMilliseconds == nil || *got.Timing.ElapsedMilliseconds != 17175 || got.Usage.SubtreeCompleteness != "complete" {
		t.Fatalf("delegation elapsed must stay the target's own boundary: %+v", got.Timing)
	}
	running := measureOpenCode(t, "ses_ekpTabGm8VtU68VtqnlYjfQs52", "", filepath.Join(records, "running.db"))
	assertOpenCode(t, running.Usage.Self, 199, 28, 115, 10496, 0)
	if running.Status != "running" || running.Timing.ElapsedMilliseconds != nil || running.Completeness == "complete" {
		t.Fatalf("background spawn completion is not the child terminal: status %s elapsed %+v completeness %s", running.Status, running.Timing.ElapsedMilliseconds, running.Completeness)
	}
	interrupted := measureOpenCode(t, "ses_MQnN3x3Ke0EtEM2I8JvaVsjZbR", "", filepath.Join(records, "interrupted.db"))
	assertOpenCode(t, interrupted.Usage.Self, 214, 65, 298, 10496, 0)
	if interrupted.Status != "interrupted" || interrupted.Timing.ElapsedMilliseconds == nil || *interrupted.Timing.ElapsedMilliseconds != 37233 {
		t.Fatalf("interrupted: status %s elapsed %+v", interrupted.Status, interrupted.Timing.ElapsedMilliseconds)
	}
}

func TestOpenCodeSQLiteBoundary(t *testing.T) {
	root := moduleRoot(t)
	derived := filepath.Join(t.TempDir(), "derived")
	command := exec.Command("python3", filepath.Join(root, "testdata/opencode-v2/native/prepare.py"), derived)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("prepare: %v\n%s", err, output)
	}
	sourceHash := sha256File(t, filepath.Join(root, "testdata/opencode-v2/native/records/override.db"))
	missingChild := measureOpenCode(t, "ses_czZw9XRushgm6LMu8axlUsfVy6", "", filepath.Join(derived, "variants/missing-descendant.db"))
	assertOpenCode(t, missingChild.Usage.Self, 1544, 131, 204, 2560, 0)
	if missingChild.Timing.ElapsedMilliseconds == nil || *missingChild.Timing.ElapsedMilliseconds != 17175 || missingChild.Usage.SubtreeCompleteness != "partial" || !hasCode(missingChild.Diagnostics, "missing_descendant") {
		t.Fatalf("missing child must keep self metrics: %+v", missingChild)
	}
	restored := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", filepath.Join(derived, "variants/missing-usage.db"))
	assertOpenCode(t, restored.Usage.Self, 10200, 66, 211, 24704, 0)
	if restored.Timing.ElapsedMilliseconds == nil || *restored.Timing.ElapsedMilliseconds != 10099 || restored.Usage.Self[0].Tokens.Total != nil {
		t.Fatalf("fresh-session cumulative fallback: %+v", restored)
	}
	prior := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "msg_D1vhyAAnkb61jlssqYaSAVIr3e", filepath.Join(derived, "variants/removed-prior-invocation.db"))
	assertOpenCode(t, prior.Usage.Self, 303, 49, 87, 24448, 0)
	duplicate := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", filepath.Join(derived, "variants/duplicate-response.db"))
	assertOpenCode(t, duplicate.Usage.Self, 10200, 66, 211, 24704, 0)
	unknown := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", filepath.Join(derived, "variants/unsupported-record.db"))
	assertOpenCode(t, unknown.Usage.Self, 10200, 66, 211, 24704, 0)
	if !hasCode(unknown.Diagnostics, "unsupported_record") || unknown.Completeness == "complete" {
		t.Fatalf("unknown message type: %+v", unknown.Diagnostics)
	}
	reversed := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", filepath.Join(derived, "variants/reversed-time.db"))
	assertOpenCode(t, reversed.Usage.Self, 10200, 66, 211, 24704, 0)
	if reversed.Timing.ElapsedMilliseconds != nil || !hasCode(reversed.Diagnostics, "invalid_boundary") {
		t.Fatalf("reversed idle: %+v", reversed.Timing)
	}
	missingModel := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", filepath.Join(derived, "variants/missing-model.db"))
	assertOpenCode(t, missingModel.Usage.Self, 10200, 66, 211, 24704, 0)
	if missingModel.Usage.Self[0].Model != nil || missingModel.Timing.ElapsedMilliseconds == nil {
		t.Fatalf("missing model: %+v", missingModel.Usage.Self)
	}
	if sha256File(t, filepath.Join(root, "testdata/opencode-v2/native/records/override.db")) != sourceHash {
		t.Fatal("prepare or reader changed the source database")
	}
	conflict := conflictingOpenCode(t, filepath.Join(root, "testdata/opencode-v2/native/records/r1.db"))
	conflicting := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", conflict)
	if !hasCode(conflicting.Diagnostics, "duplicate_conflict") || conflicting.Completeness != "partial" || sumCounter(conflicting.Usage.Self, func(tokens report.Tokens) *int64 { return tokens.Input }) >= 10200 {
		t.Fatalf("conflicting response must not be chosen: %+v", conflicting.Usage.Self)
	}
	if _, err := measureOpenCodeResult(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", filepath.Join(derived, "lookup/invalid.db")); err != nil {
		t.Fatal(err)
	}
	invalid := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", filepath.Join(derived, "lookup/invalid.db"))
	if invalid.Completeness != "incomplete" || !hasCode(invalid.Diagnostics, "malformed_record") {
		t.Fatalf("invalid database: %+v", invalid.Diagnostics)
	}
	empty := measureOpenCode(t, "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "", filepath.Join(derived, "lookup/empty.db"))
	if empty.Completeness != "incomplete" || !hasCode(empty.Diagnostics, "agent_not_found") || len(empty.Usage.Self) != 0 {
		t.Fatalf("empty database: %+v", empty)
	}
	wal, mainOnly := walDatabase(t)
	visible := measureOpenCode(t, "ses_wal", "", wal)
	assertOpenCode(t, visible.Usage.Self, 5, 1, 0, 0, 0)
	hidden := measureOpenCode(t, "ses_wal", "", mainOnly)
	if len(hidden.Usage.Self) != 0 {
		t.Fatalf("a copied database without its WAL must not show the uncheckpointed row: %+v", hidden.Usage.Self)
	}
}

func measureOpenCode(t *testing.T, agent, execution, records string) report.ExecutionReport {
	t.Helper()
	result, err := measureOpenCodeResult(t, agent, execution, records)
	if err != nil {
		t.Fatal(err)
	}
	return report.Collect("opencode-v2", adapter.Request{Agent: agent, Execution: execution}, result)
}

func measureOpenCodeResult(t *testing.T, agent, execution, records string) (adapter.ReadResult, error) {
	t.Helper()
	reader, err := adapter.New("opencode-v2")
	if err != nil {
		t.Fatal(err)
	}
	return reader.Read(adapter.Request{Agent: agent, Execution: execution, Records: records})
}

func assertOpenCode(t *testing.T, usage []report.ModelUsage, input, output, reasoning, cacheRead, cacheWrite int64) {
	t.Helper()
	if sumCounter(usage, func(tokens report.Tokens) *int64 { return tokens.Input }) != input ||
		sumCounter(usage, func(tokens report.Tokens) *int64 { return tokens.Output }) != output ||
		sumCounter(usage, func(tokens report.Tokens) *int64 { return tokens.Reasoning }) != reasoning ||
		sumCounter(usage, func(tokens report.Tokens) *int64 { return tokens.CacheRead }) != cacheRead ||
		sumCounter(usage, func(tokens report.Tokens) *int64 { return tokens.CacheWrite }) != cacheWrite ||
		sumCounter(usage, func(tokens report.Tokens) *int64 { return tokens.Total }) != -1 {
		t.Fatalf("tokens got %+v", usage)
	}
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return string(sum[:])
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func conflictingOpenCode(t *testing.T, source string) string {
	t.Helper()
	destination := filepath.Join(t.TempDir(), "conflict.db")
	copyFile(t, source, destination)
	db, err := sql.Open("sqlite", destination)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var data string
	if err := db.QueryRow(`SELECT data FROM session_message WHERE id=?`, "msg_2ou44EfXGCu7cZCEaovIsRGV3h").Scan(&data); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		t.Fatal(err)
	}
	tokens := payload["tokens"].(map[string]any)
	tokens["input"] = 1
	rewritten, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"msg_conflict", "ses_PB9VT4cvOsLHQ6DcmPDil2WAEe", "assistant", 6, 1767269838428, 1767269838428, string(rewritten)); err != nil {
		t.Fatal(err)
	}
	return destination
}

func walDatabase(t *testing.T) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wal.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE session_v2 (id TEXT PRIMARY KEY, parent_id TEXT, model TEXT, time_created INTEGER, tokens_input INTEGER, tokens_output INTEGER, tokens_reasoning INTEGER, tokens_cache_read INTEGER, tokens_cache_write INTEGER)`,
		`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session_v2 (id, parent_id, model, time_created, tokens_input, tokens_output, tokens_reasoning, tokens_cache_read, tokens_cache_write) VALUES ('ses_wal', NULL, '{"id":"m","providerID":"openai"}', 1000, NULL, NULL, NULL, NULL, NULL)`,
		`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES ('msg_user', 'ses_wal', 'user', 1, 1000, 1000, '{"time":{"created":1000}}')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES ('msg_assistant', 'ses_wal', 'assistant', 2, 1100, 1100, '{"time":{"created":1100},"model":{"id":"m","providerID":"openai"},"native":{"responseId":"resp-wal"},"tokens":{"input":5,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES ('msg_idle', 'ses_wal', 'idle', 3, 1500, 1500, '{"time":{"created":1500},"outcome":"succeeded"}')`); err != nil {
		t.Fatal(err)
	}
	mainOnly := filepath.Join(t.TempDir(), "main-only.db")
	copyFile(t, path, mainOnly)
	return path, mainOnly
}

func writeOpenCodeDB(t *testing.T, path string, statements []string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
