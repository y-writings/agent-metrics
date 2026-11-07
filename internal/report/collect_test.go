package report

import (
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
