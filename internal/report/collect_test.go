package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/y-writings/agent-metrics/internal/adapter"
)

func TestCollectExecutionTreeWithIndependentTurnIDs(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	es := []adapter.Event{
		{ID: "old", Session: "agent", Execution: "old-run", Task: "turn-old", Kind: "usage", Model: "m", Input: p(100)},
		{ID: "start", Session: "agent", Execution: "run", Task: "turn-a", Kind: "execution_start", Time: t0},
		{ID: "self", Session: "agent", Execution: "run", Task: "turn-a", Kind: "usage", Model: "m", Input: p(2)},
		{ID: "child-start", Session: "child", Execution: "child-run", ParentExecution: "run", Task: "unrelated-turn", Kind: "execution_start", Time: t0},
		{ID: "child-use", Session: "child", Execution: "child-run", ParentExecution: "run", Task: "unrelated-turn", Kind: "usage", Model: "m", Input: p(3)},
		{ID: "child-end", Session: "child", Execution: "child-run", ParentExecution: "run", Kind: "execution_end", Time: t0.Add(time.Second)},
		{ID: "end", Session: "agent", Execution: "run", Task: "turn-a", Kind: "execution_end", Time: t0.Add(2 * time.Second)},
	}
	r := Collect("codex", "agent", "run", es, nil)
	if r.SchemaVersion != "2.0" || len(r.Executions) != 2 || *r.Usage.Self[0].Tokens.Input != 2 || *r.Usage.Subtree[0].Tokens.Input != 5 {
		t.Fatalf("unexpected report: %+v", r)
	}
	if r.Timing.ElapsedMilliseconds == nil || *r.Timing.ElapsedMilliseconds != 2000 {
		t.Fatalf("timing: %+v", r.Timing)
	}
	if r.Target.SessionID != nil {
		t.Fatalf("missing source session must remain null: %+v", r.Target)
	}
}

func TestCollectScopesFallbackExecutionToAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	data := `{"id":"start-a","agent_id":"a","session_id":"source-session","task_id":"shared","kind":"task_start","timestamp":"2026-01-01T00:00:00Z"}
{"id":"use-a","agent_id":"a","task_id":"shared","kind":"usage","model":"m","usage":{"input":2}}
{"id":"end-a","agent_id":"a","task_id":"shared","kind":"task_end","timestamp":"2026-01-01T00:00:01Z"}
{"id":"use-b","agent_id":"b","task_id":"shared","kind":"usage","model":"m","usage":{"input":99}}
{"id":"use-c","agent_id":"c","execution_id":"shared","kind":"usage","model":"m","usage":{"input":100}}
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := adapter.New("codex")
	if err != nil {
		t.Fatal(err)
	}
	events, warnings, err := a.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	r := Collect(a.Name(), "a", "", events, warnings)
	if r.Completeness != "complete" || r.Target.AgentID != "a" || r.Target.SessionID == nil || *r.Target.SessionID != "source-session" || len(r.Executions) != 1 || r.Executions[0].AgentID != "a" || len(r.Executions[0].SourceIDs) != 3 {
		t.Fatalf("shared fallback ID must select only agent a: %+v", r)
	}
	if len(r.Usage.Self) != 1 || r.Usage.Self[0].Tokens.Input == nil || *r.Usage.Self[0].Tokens.Input != 2 || len(r.Usage.Subtree) != 1 || *r.Usage.Subtree[0].Tokens.Input != 2 {
		t.Fatalf("unrelated agents must not contribute usage: %+v", r.Usage)
	}
}

func TestCollectDiagnosesUnscopedUsageWithoutAttributingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	data := `{"id":"start","agent_id":"parent","execution_id":"run","kind":"execution_start","timestamp":"2026-01-01T00:00:00Z"}
{"id":"parent-use","agent_id":"parent","execution_id":"run","kind":"usage","model":"m","usage":{"input":2}}
{"id":"parent-unscoped","agent_id":"parent","kind":"usage","model":"m","usage":{"input":99}}
{"id":"child-start","agent_id":"child","execution_id":"child-run","parent_execution_id":"run","kind":"execution_start","timestamp":"2026-01-01T00:00:00Z"}
{"id":"child-use","agent_id":"child","execution_id":"child-run","kind":"usage","model":"m","usage":{"input":3}}
{"id":"child-unscoped","agent_id":"child","kind":"assistant","model":"m","usage":{"input":50}}
{"id":"child-end","agent_id":"child","execution_id":"child-run","kind":"execution_end","timestamp":"2026-01-01T00:00:01Z"}
{"id":"end","agent_id":"parent","execution_id":"run","kind":"execution_end","timestamp":"2026-01-01T00:00:02Z"}
{"id":"other-unscoped","agent_id":"other","kind":"usage","model":"m","usage":{"input":100}}
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := adapter.New("codex")
	if err != nil {
		t.Fatal(err)
	}
	events, warnings, err := a.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	r := Collect(a.Name(), "parent", "run", events, warnings)
	if r.Completeness != "partial" || len(r.Usage.Self) != 1 || *r.Usage.Self[0].Tokens.Input != 2 || len(r.Usage.Subtree) != 1 || *r.Usage.Subtree[0].Tokens.Input != 5 {
		t.Fatalf("unscoped usage must remain unattributed and make the report partial: %+v", r)
	}
	if len(r.Diagnostics) != 2 || r.Diagnostics[0].Code != "unscoped_usage" || r.Diagnostics[1].Code != "unscoped_usage" {
		t.Fatalf("only included agents' unscoped usage should be diagnosed: %+v", r.Diagnostics)
	}
}

func TestCollectProjectsReferencedSpawnOntoChild(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	es := []adapter.Event{
		{ID: "start", Session: "parent", Execution: "run", Kind: "execution_start", Time: t0},
		{ID: "parent-use", Session: "parent", Execution: "run", Kind: "usage", Model: "m", Input: p(2)},
		{ID: "spawn", Session: "parent", Execution: "run", ReferencedExecution: "child-run", Kind: "spawn", Time: t0.Add(time.Second)},
		{ID: "child-start", Session: "child", Execution: "child-run", Kind: "execution_start", Time: t0.Add(time.Second)},
		{ID: "child-use", Session: "child", Execution: "child-run", Kind: "usage", Model: "m", Input: p(3)},
		{ID: "child-end", Session: "child", Execution: "child-run", Kind: "execution_end", Time: t0.Add(2 * time.Second)},
		{ID: "end", Session: "parent", Execution: "run", Kind: "execution_end", Time: t0.Add(3 * time.Second)},
	}
	r := Collect("codex", "parent", "run", es, nil)
	if r.Completeness != "complete" || len(r.Executions) != 2 || len(r.Usage.Subtree) != 1 || *r.Usage.Subtree[0].Tokens.Input != 5 {
		t.Fatalf("referenced child must be included: %+v", r)
	}
	for _, x := range r.Executions {
		switch x.ID {
		case "run":
			if x.SpawnedAt != nil {
				t.Fatalf("parent must not inherit child spawn: %+v", x)
			}
		case "child-run":
			if x.SpawnedAt == nil || !x.SpawnedAt.Equal(t0.Add(time.Second)) || x.ParentExecutionID == nil || *x.ParentExecutionID != "run" {
				t.Fatalf("child must inherit referenced spawn metadata: %+v", x)
			}
		}
	}
	child := Collect("codex", "child", "child-run", es, nil)
	if child.Completeness != "complete" || len(child.Executions) != 1 || child.Executions[0].ID != "child-run" || child.Executions[0].SpawnedAt == nil || !child.Executions[0].SpawnedAt.Equal(t0.Add(time.Second)) || child.Executions[0].ParentExecutionID == nil || *child.Executions[0].ParentExecutionID != "run" {
		t.Fatalf("child-only report must retain external spawn metadata: %+v", child)
	}
	if len(child.Usage.Self) != 1 || len(child.Usage.Subtree) != 1 || *child.Usage.Subtree[0].Tokens.Input != 3 {
		t.Fatalf("child-only report must exclude parent usage: %+v", child.Usage)
	}
}

func TestCollectTimingSourcesMatchSelectedBoundaries(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := Collect("codex", "agent", "run", []adapter.Event{
		{ID: "undated-start", Session: "agent", Execution: "run", Kind: "execution_start"},
		{ID: "late-start", Session: "agent", Execution: "run", Kind: "execution_start", Time: t0.Add(time.Second)},
		{ID: "early-start", Session: "agent", Execution: "run", Kind: "execution_start", Time: t0},
		{ID: "undated-end", Session: "agent", Execution: "run", Kind: "execution_end"},
		{ID: "late-end", Session: "agent", Execution: "run", Kind: "execution_end", Time: t0.Add(3 * time.Second), Status: "failed"},
		{ID: "early-end", Session: "agent", Execution: "run", Kind: "execution_end", Time: t0.Add(2 * time.Second)},
		{ID: "usage", Session: "agent", Execution: "run", Kind: "usage", Model: "m", Input: p(1)},
	}, nil)
	if r.Timing.StartedAt == nil || !r.Timing.StartedAt.Equal(t0) || r.Timing.StartSourceID == nil || *r.Timing.StartSourceID != "early-start" || r.Timing.EndedAt == nil || !r.Timing.EndedAt.Equal(t0.Add(3*time.Second)) || r.Timing.EndSourceID == nil || *r.Timing.EndSourceID != "late-end" || r.Status != "failed" {
		t.Fatalf("timing sources must match selected boundaries: %+v", r.Timing)
	}
}

func TestCollectDoesNotGuessAmbiguousDescendant(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := Collect("codex", "parent", "run", []adapter.Event{
		{ID: "start", Session: "parent", Execution: "run", Kind: "execution_start", Time: t0},
		{ID: "use", Session: "parent", Execution: "run", Kind: "usage", Model: "m", Input: p(2)},
		{ID: "spawn", Session: "parent", Execution: "run", ReferencedExecution: "shared", Kind: "spawn", Time: t0},
		{ID: "end", Session: "parent", Execution: "run", Kind: "execution_end", Time: t0.Add(time.Second)},
		{ID: "child-a", Session: "a", Execution: "shared", Kind: "usage", Model: "m", Input: p(3)},
		{ID: "child-b", Session: "b", Execution: "shared", Kind: "usage", Model: "m", Input: p(99)},
	}, nil)
	if r.Completeness != "partial" || len(r.Executions) != 1 || len(r.Usage.Subtree) != 1 || *r.Usage.Subtree[0].Tokens.Input != 2 {
		t.Fatalf("ambiguous child cannot be guessed or reported complete: %+v", r)
	}
	if len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != "ambiguous_execution_link" {
		t.Fatalf("expected ambiguity diagnostic: %+v", r.Diagnostics)
	}
}

func TestCollectRequiresExecutionForReusedAgent(t *testing.T) {
	r := Collect("codex", "agent", "", []adapter.Event{{Session: "agent", Execution: "one"}, {Session: "agent", Execution: "two"}}, nil)
	if r.Completeness != "incomplete" || len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != "ambiguous_execution" || !strings.Contains(r.Diagnostics[0].Message, "one") {
		t.Fatalf("unexpected report: %+v", r)
	}
}

func TestCollectDiagnosesReferencedExecutionWithoutRecords(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := Collect("codex", "agent", "run", []adapter.Event{
		{ID: "start", Session: "agent", Execution: "run", Kind: "execution_start", Time: t0},
		{ID: "spawn", Session: "agent", Execution: "run", ReferencedExecution: "missing", Kind: "spawn"},
		{ID: "usage", Session: "agent", Execution: "run", Kind: "usage", Model: "m", Input: p(0)},
		{ID: "end", Session: "agent", Execution: "run", Kind: "execution_end", Time: t0},
	}, nil)
	if r.Completeness != "partial" {
		t.Fatalf("missing child must make subtree partial: %+v", r)
	}
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == "missing_descendant" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing descendant diagnostic absent: %+v", r.Diagnostics)
	}
}
