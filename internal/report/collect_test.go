package report

import (
	"strings"
	"testing"
	"time"

	"github.com/y-writings/agent-metrics/internal/adapter"
)

func p(n int64) *int64 { return &n }

func collectEvents(product, agent, execution string, events []adapter.Event) ExecutionReport {
	return Collect(product, adapter.Request{Agent: agent, Execution: execution}, adapter.ReadResult{AgentID: agent, Events: events})
}

func TestCollectExecutionTreeWithIndependentTurnIDs(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []adapter.Event{
		{ID: "old", Session: "agent", Execution: "old-run", Task: "turn-old", Kind: "usage", Model: "m", Input: p(100)},
		{ID: "start", Session: "agent", Execution: "run", Task: "turn-a", Kind: "execution_start", Time: t0},
		{ID: "self", Session: "agent", Execution: "run", Task: "turn-a", Kind: "usage", Model: "m", Input: p(2)},
		{ID: "child-start", Session: "child", Execution: "child-run", ParentExecution: "run", Task: "unrelated-turn", Kind: "execution_start", Time: t0},
		{ID: "child-use", Session: "child", Execution: "child-run", ParentExecution: "run", Task: "unrelated-turn", Kind: "usage", Model: "m", Input: p(3)},
		{ID: "child-end", Session: "child", Execution: "child-run", ParentExecution: "run", Kind: "execution_end", Time: t0.Add(time.Second)},
		{ID: "end", Session: "agent", Execution: "run", Task: "turn-a", Kind: "execution_end", Time: t0.Add(2 * time.Second)},
	}
	got := collectEvents("codex", "agent", "run", events)
	if got.SchemaVersion != "2.1" || len(got.Executions) != 2 || *got.Usage.Self[0].Tokens.Input != 2 || *got.Usage.Subtree[0].Tokens.Input != 5 {
		t.Fatalf("unexpected report: %+v", got)
	}
	if got.Timing.ElapsedMilliseconds == nil || *got.Timing.ElapsedMilliseconds != 2000 {
		t.Fatalf("timing: %+v", got.Timing)
	}
	if got.Target.SessionID != nil {
		t.Fatalf("missing source session must remain null: %+v", got.Target)
	}
	if got.Usage.SelfCompleteness != "complete" || got.Usage.SubtreeCompleteness != "complete" {
		t.Fatalf("complete tree: %+v", got.Usage)
	}
}

func TestCollectScopesSharedExecutionIDToAgent(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []adapter.Event{
		{ID: "start-a", Session: "a", SourceSession: "source-session", Execution: "shared", Kind: "execution_start", Time: t0},
		{ID: "use-a", Session: "a", Execution: "shared", Kind: "usage", Model: "m", Input: p(2)},
		{ID: "end-a", Session: "a", Execution: "shared", Kind: "execution_end", Time: t0.Add(time.Second)},
		{ID: "use-b", Session: "b", Execution: "shared", Kind: "usage", Model: "m", Input: p(99)},
		{ID: "use-c", Session: "c", Execution: "shared", Kind: "usage", Model: "m", Input: p(100)},
	}
	got := collectEvents("codex", "a", "", events)
	if got.Completeness != "complete" || got.Target.AgentID != "a" || got.Target.SessionID == nil || *got.Target.SessionID != "source-session" || len(got.Executions) != 1 || got.Executions[0].AgentID != "a" || len(got.Executions[0].SourceIDs) != 3 {
		t.Fatalf("shared execution ID must select only agent a: %+v", got)
	}
	if len(got.Usage.Self) != 1 || got.Usage.Self[0].Tokens.Input == nil || *got.Usage.Self[0].Tokens.Input != 2 || len(got.Usage.Subtree) != 1 || *got.Usage.Subtree[0].Tokens.Input != 2 {
		t.Fatalf("unrelated agents must not contribute usage: %+v", got.Usage)
	}
}

func TestCollectDiagnosesUnscopedUsageWithoutAttributingIt(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []adapter.Event{
		{ID: "start", Session: "parent", Execution: "run", Kind: "execution_start", Time: t0},
		{ID: "parent-use", Session: "parent", Execution: "run", Kind: "usage", Model: "m", Input: p(2)},
		{ID: "parent-unscoped", Session: "parent", Kind: "usage", Model: "m", Input: p(99)},
		{ID: "child-start", Session: "child", Execution: "child-run", ParentExecution: "run", Kind: "execution_start", Time: t0},
		{ID: "child-use", Session: "child", Execution: "child-run", Kind: "usage", Model: "m", Input: p(3)},
		{ID: "child-unscoped", Session: "child", Kind: "assistant", Model: "m", Input: p(50)},
		{ID: "child-end", Session: "child", Execution: "child-run", Kind: "execution_end", Time: t0.Add(time.Second)},
		{ID: "end", Session: "parent", Execution: "run", Kind: "execution_end", Time: t0.Add(2 * time.Second)},
		{ID: "other-unscoped", Session: "other", Kind: "usage", Model: "m", Input: p(100)},
	}
	got := collectEvents("codex", "parent", "run", events)
	if got.Completeness != "partial" || len(got.Usage.Self) != 1 || *got.Usage.Self[0].Tokens.Input != 2 || len(got.Usage.Subtree) != 1 || *got.Usage.Subtree[0].Tokens.Input != 5 {
		t.Fatalf("unscoped usage must remain unattributed and make the report partial: %+v", got)
	}
	if len(got.UnattributedUsage) != 2 || got.UnattributedUsage[0].SourceID != "parent-unscoped" || *got.UnattributedUsage[0].Tokens.Input != 99 {
		t.Fatalf("unscoped counters must be retained outside the sums: %+v", got.UnattributedUsage)
	}
	if len(got.Diagnostics) != 2 || got.Diagnostics[0].Code != "unscoped_usage" || got.Diagnostics[1].Code != "unscoped_usage" {
		t.Fatalf("only included agents' unscoped usage should be diagnosed: %+v", got.Diagnostics)
	}
}

func TestCollectProjectsReferencedSpawnOntoChild(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []adapter.Event{
		{ID: "start", Session: "parent", Execution: "run", Kind: "execution_start", Time: t0},
		{ID: "parent-use", Session: "parent", Execution: "run", Kind: "usage", Model: "m", Input: p(2)},
		{ID: "spawn", Session: "parent", Execution: "run", ReferencedExecution: "child-run", Kind: "spawn", Time: t0.Add(time.Second)},
		{ID: "child-start", Session: "child", Execution: "child-run", Kind: "execution_start", Time: t0.Add(time.Second)},
		{ID: "child-use", Session: "child", Execution: "child-run", Kind: "usage", Model: "m", Input: p(3)},
		{ID: "child-end", Session: "child", Execution: "child-run", Kind: "execution_end", Time: t0.Add(2 * time.Second)},
		{ID: "end", Session: "parent", Execution: "run", Kind: "execution_end", Time: t0.Add(3 * time.Second)},
	}
	got := collectEvents("codex", "parent", "run", events)
	if got.Completeness != "complete" || len(got.Executions) != 2 || len(got.Usage.Subtree) != 1 || *got.Usage.Subtree[0].Tokens.Input != 5 {
		t.Fatalf("referenced child must be included: %+v", got)
	}
	for _, execution := range got.Executions {
		switch execution.ID {
		case "run":
			if execution.SpawnedAt != nil {
				t.Fatalf("parent must not inherit child spawn: %+v", execution)
			}
		case "child-run":
			if execution.SpawnedAt == nil || !execution.SpawnedAt.Equal(t0.Add(time.Second)) || execution.ParentExecutionID == nil || *execution.ParentExecutionID != "run" {
				t.Fatalf("child must inherit referenced spawn metadata: %+v", execution)
			}
		}
	}
	child := collectEvents("codex", "child", "child-run", events)
	if child.Completeness != "complete" || len(child.Executions) != 1 || child.Executions[0].ID != "child-run" || child.Executions[0].SpawnedAt == nil || !child.Executions[0].SpawnedAt.Equal(t0.Add(time.Second)) || child.Executions[0].ParentExecutionID == nil || *child.Executions[0].ParentExecutionID != "run" {
		t.Fatalf("child-only report must retain external spawn metadata: %+v", child)
	}
	if len(child.Usage.Self) != 1 || len(child.Usage.Subtree) != 1 || *child.Usage.Subtree[0].Tokens.Input != 3 {
		t.Fatalf("child-only report must exclude parent usage: %+v", child.Usage)
	}
}

func TestCollectTimingSourcesMatchSelectedBoundaries(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := collectEvents("codex", "agent", "run", []adapter.Event{
		{ID: "undated-start", Session: "agent", Execution: "run", Kind: "execution_start"},
		{ID: "late-start", Session: "agent", Execution: "run", Kind: "execution_start", Time: t0.Add(time.Second)},
		{ID: "early-start", Session: "agent", Execution: "run", Kind: "execution_start", Time: t0},
		{ID: "undated-end", Session: "agent", Execution: "run", Kind: "execution_end"},
		{ID: "late-end", Session: "agent", Execution: "run", Kind: "execution_end", Time: t0.Add(3 * time.Second), Status: "failed"},
		{ID: "early-end", Session: "agent", Execution: "run", Kind: "execution_end", Time: t0.Add(2 * time.Second)},
		{ID: "later-state", Session: "agent", Execution: "run", Kind: "status", Time: t0.Add(4 * time.Second), Status: "interrupted"},
		{ID: "usage", Session: "agent", Execution: "run", Kind: "usage", Model: "m", Input: p(1)},
	})
	if got.Timing.StartedAt == nil || !got.Timing.StartedAt.Equal(t0) || got.Timing.StartSourceID == nil || *got.Timing.StartSourceID != "early-start" || got.Timing.EndedAt == nil || !got.Timing.EndedAt.Equal(t0.Add(3*time.Second)) || got.Timing.EndSourceID == nil || *got.Timing.EndSourceID != "late-end" || got.Status != "failed" {
		t.Fatalf("timing sources must match selected boundaries: %+v status %s", got.Timing, got.Status)
	}
}

func TestCollectRetainsLatestExplicitStatusWithoutTerminal(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := collectEvents("codex", "agent", "run", []adapter.Event{
		{ID: "latest-status", Session: "agent", Execution: "run", Kind: "status", Status: "interrupted", Time: t0.Add(2 * time.Second)},
		{ID: "start", Session: "agent", Execution: "run", Kind: "execution_start", Status: "in_progress", Time: t0},
		{ID: "older-status", Session: "agent", Execution: "run", Kind: "status", Status: "running", Time: t0.Add(time.Second)},
		{ID: "usage", Session: "agent", Execution: "run", Kind: "usage", Model: "m", Input: p(2)},
	})
	if got.Status != "interrupted" || len(got.Executions) != 1 || got.Executions[0].Status != "interrupted" {
		t.Fatalf("latest explicit status must survive a missing terminal event: %+v", got)
	}
	if got.Completeness != "partial" || got.Timing.EndedAt != nil || got.Timing.ElapsedMilliseconds != nil {
		t.Fatalf("status must not stand in for an explicit terminal boundary: %+v", got)
	}
	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "missing_boundary" {
		t.Fatalf("missing terminal boundary must be diagnosed: %+v", got.Diagnostics)
	}
}

func TestCollectDoesNotGuessAmbiguousDescendant(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := collectEvents("codex", "parent", "run", []adapter.Event{
		{ID: "start", Session: "parent", Execution: "run", Kind: "execution_start", Time: t0},
		{ID: "use", Session: "parent", Execution: "run", Kind: "usage", Model: "m", Input: p(2)},
		{ID: "spawn", Session: "parent", Execution: "run", ReferencedExecution: "shared", Kind: "spawn", Time: t0},
		{ID: "end", Session: "parent", Execution: "run", Kind: "execution_end", Time: t0.Add(time.Second)},
		{ID: "child-a", Session: "a", Execution: "shared", Kind: "usage", Model: "m", Input: p(3)},
		{ID: "child-b", Session: "b", Execution: "shared", Kind: "usage", Model: "m", Input: p(99)},
	})
	if got.Completeness != "partial" || len(got.Executions) != 1 || len(got.Usage.Subtree) != 1 || *got.Usage.Subtree[0].Tokens.Input != 2 {
		t.Fatalf("ambiguous child cannot be guessed or reported complete: %+v", got)
	}
	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "ambiguous_execution_link" {
		t.Fatalf("expected ambiguity diagnostic: %+v", got.Diagnostics)
	}
}

func TestCollectRequiresExecutionForReusedAgent(t *testing.T) {
	got := collectEvents("codex", "agent", "", []adapter.Event{{Session: "agent", Execution: "one"}, {Session: "agent", Execution: "two"}})
	if got.Completeness != "incomplete" || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "ambiguous_execution" || !strings.Contains(got.Diagnostics[0].Message, "one") || got.Target.ExecutionID != nil {
		t.Fatalf("unexpected report: %+v", got)
	}
}

func TestCollectDiagnosesReferencedExecutionWithoutRecords(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := collectEvents("codex", "agent", "run", []adapter.Event{
		{ID: "start", Session: "agent", Execution: "run", Kind: "execution_start", Time: t0},
		{ID: "spawn", Session: "agent", Execution: "run", ReferencedExecution: "missing", Kind: "spawn"},
		{ID: "usage", Session: "agent", Execution: "run", Kind: "usage", Model: "m", Input: p(0)},
		{ID: "end", Session: "agent", Execution: "run", Kind: "execution_end", Time: t0},
	})
	if got.Completeness != "partial" || got.Usage.SelfCompleteness != "complete" || *got.Usage.Self[0].Tokens.Input != 0 || got.Timing.ElapsedMilliseconds == nil || *got.Timing.ElapsedMilliseconds != 0 {
		t.Fatalf("missing child must keep known self usage and zero elapsed: %+v", got)
	}
	found := false
	for _, diagnostic := range got.Diagnostics {
		if diagnostic.Code == "missing_descendant" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing descendant diagnostic absent: %+v", got.Diagnostics)
	}
}

func TestCollectNativeIdentityAndPartialScopes(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t.Run("returned reference differs from native id", func(t *testing.T) {
		got := Collect("codex", adapter.Request{Agent: "/root/capture_target"}, adapter.ReadResult{
			AgentID: "thread-1",
			Events: []adapter.Event{
				{ID: "start", Session: "thread-1", SourceSession: "thread-1", Execution: "turn-1", Kind: "execution_start", Time: t0, StartDefinition: "task_started envelope timestamp"},
				{ID: "use", Session: "thread-1", Execution: "turn-1", Kind: "usage", UsageID: "resp-1", Provider: "openai", Model: "gpt", Input: p(10), Total: p(10)},
				{ID: "end", Session: "thread-1", Execution: "turn-1", Kind: "execution_end", Time: t0.Add(time.Second), Status: "completed", EndDefinition: "task_complete envelope timestamp"},
			},
		})
		if got.Target.AgentID != "/root/capture_target" || got.Target.ExecutionID == nil || *got.Target.ExecutionID != "turn-1" || got.Target.SessionID == nil || *got.Target.SessionID != "thread-1" {
			t.Fatalf("identity: %+v", got.Target)
		}
		if got.Completeness != "complete" || *got.Usage.Self[0].Tokens.Total != 10 || *got.Timing.ElapsedMilliseconds != 1000 {
			t.Fatalf("measurement: %+v", got)
		}
	})

	t.Run("missing child agent keeps self and elapsed", func(t *testing.T) {
		got := Collect("codex", adapter.Request{Agent: "parent", Execution: "run"}, adapter.ReadResult{
			AgentID: "parent",
			Events: []adapter.Event{
				{ID: "start", Session: "parent", Execution: "run", Kind: "execution_start", Time: t0},
				{ID: "use", Session: "parent", Execution: "run", Kind: "usage", UsageID: "u", Provider: "openai", Model: "m", Input: p(10), Total: p(10)},
				{ID: "spawn", Session: "parent", Execution: "run", Kind: "spawn"},
				{ID: "end", Session: "parent", Execution: "run", Kind: "execution_end", Time: t0.Add(time.Second), Status: "completed"},
			},
			Delegations: []adapter.Delegation{{
				ParentAgentID: "parent",
				ChildAgentID:  "child-only",
				SourceID:      "spawn",
				Parent:        &adapter.ExecutionRef{AgentID: "parent", ExecutionID: "run"},
			}},
		})
		if got.Completeness != "partial" || got.Usage.SelfCompleteness != "complete" || got.Usage.SubtreeCompleteness != "partial" {
			t.Fatalf("scopes: %+v completeness %s", got.Usage, got.Completeness)
		}
		if *got.Usage.Self[0].Tokens.Total != 10 || *got.Usage.Subtree[0].Tokens.Total != 10 || *got.Timing.ElapsedMilliseconds != 1000 || len(got.Executions) != 1 {
			t.Fatalf("self measurement changed: %+v", got)
		}
		found := false
		for _, diagnostic := range got.Diagnostics {
			if diagnostic.Code == "missing_descendant" && diagnostic.RelatedAgentID == "child-only" && diagnostic.Scope == "execution" {
				found = true
			}
		}
		if !found {
			t.Fatalf("child agent reference was dropped: %+v", got.Diagnostics)
		}
	})

	t.Run("missing execution id keeps known usage", func(t *testing.T) {
		got := Collect("codex", adapter.Request{Agent: "/root/capture_target"}, adapter.ReadResult{
			AgentID: "thread-1",
			Events: []adapter.Event{
				{ID: "use", Session: "thread-1", Kind: "usage", UsageID: "resp", Provider: "openai", Model: "m", Input: p(8), Total: p(8)},
			},
		})
		if got.Target.ExecutionID != nil || got.Target.AgentID != "/root/capture_target" || got.Target.SessionID == nil || *got.Target.SessionID != "thread-1" {
			t.Fatalf("must not invent an execution id: %+v", got.Target)
		}
		if len(got.Usage.Self) != 0 || len(got.UnattributedUsage) != 1 || *got.UnattributedUsage[0].Tokens.Total != 8 {
			t.Fatalf("known usage must be retained without entering self: %+v", got)
		}
		if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "missing_execution_id" || got.Diagnostics[0].Scope != "agent" || got.Diagnostics[0].AgentID != "thread-1" || got.Diagnostics[0].ExecutionID != nil {
			t.Fatalf("agent-scoped diagnostic: %+v", got.Diagnostics)
		}
	})

	t.Run("unknown provider and model keep counters", func(t *testing.T) {
		provider := Collect("claude-code", adapter.Request{Agent: "agent", Execution: "run"}, adapter.ReadResult{
			AgentID: "agent",
			Events: []adapter.Event{
				{ID: "start", Session: "agent", Execution: "run", Kind: "execution_start", Time: t0},
				{ID: "use", Session: "agent", Execution: "run", Kind: "usage", UsageID: "msg", Model: "claude", Input: p(5), Total: p(5)},
				{ID: "end", Session: "agent", Execution: "run", Kind: "execution_end", Time: t0.Add(time.Second)},
			},
			Diagnostics: []adapter.Diagnostic{{
				Code: "unknown_provider", Message: "saved transcript has no provider", Scope: "execution", Metric: "provider",
				AgentID: "agent", Execution: &adapter.ExecutionRef{AgentID: "agent", ExecutionID: "run"},
			}},
		})
		if provider.Completeness != "complete" || provider.Usage.SelfCompleteness != "complete" || provider.Usage.Self[0].Provider != nil || provider.Usage.Self[0].Model == nil || *provider.Usage.Self[0].Model != "claude" || *provider.Usage.Self[0].Tokens.Total != 5 {
			t.Fatalf("unknown provider must keep the model and counters: %+v", provider)
		}
		model := Collect("claude-code", adapter.Request{Agent: "agent", Execution: "run"}, adapter.ReadResult{
			AgentID: "agent",
			Events: []adapter.Event{
				{ID: "start", Session: "agent", Execution: "run", Kind: "execution_start", Time: t0},
				{ID: "use", Session: "agent", Execution: "run", Kind: "usage", UsageID: "msg", Input: p(5), Total: p(5)},
				{ID: "end", Session: "agent", Execution: "run", Kind: "execution_end", Time: t0.Add(time.Second)},
			},
		})
		if model.Completeness != "partial" || model.Usage.SelfCompleteness != "partial" || *model.Timing.ElapsedMilliseconds != 1000 || model.Usage.Self[0].Model != nil || *model.Usage.Self[0].Tokens.Total != 5 {
			t.Fatalf("unknown model must keep counters and elapsed time: %+v", model)
		}
	})
}

func TestCollectDiagnosticScope(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	complete := []adapter.Event{
		{ID: "start", Session: "agent", Execution: "run", Kind: "execution_start", Time: t0},
		{ID: "use", Session: "agent", Execution: "run", Kind: "usage", UsageID: "u", Provider: "openai", Model: "m", Input: p(2), Total: p(2)},
		{ID: "end", Session: "agent", Execution: "run", Kind: "execution_end", Time: t0.Add(time.Second)},
	}
	t.Run("foreign execution and agent diagnostics are excluded", func(t *testing.T) {
		got := Collect("codex", adapter.Request{Agent: "agent", Execution: "run"}, adapter.ReadResult{
			AgentID: "agent",
			Events:  complete,
			Diagnostics: []adapter.Diagnostic{
				{Code: "unknown_usage", Message: "other execution", Scope: "execution", AgentID: "other", Execution: &adapter.ExecutionRef{AgentID: "other", ExecutionID: "other-run"}},
				{Code: "missing_execution_id", Message: "stranger lost its execution", Scope: "agent", AgentID: "stranger"},
			},
		})
		if got.Completeness != "complete" || got.Usage.SelfCompleteness != "complete" || len(got.Diagnostics) != 0 {
			t.Fatalf("foreign findings must not partial the selection: %+v", got)
		}
	})
	t.Run("source agent and execution scopes stay distinct", func(t *testing.T) {
		source := Collect("codex", adapter.Request{Agent: "agent"}, adapter.ReadResult{
			Diagnostics: []adapter.Diagnostic{{Code: "source_not_found", Message: "records are unavailable", Scope: "source"}},
		})
		if source.Completeness != "incomplete" || len(source.Diagnostics) != 1 || source.Diagnostics[0].Scope != "source" || source.Diagnostics[0].Code != "source_not_found" {
			t.Fatalf("source failure: %+v", source)
		}
		agent := Collect("codex", adapter.Request{Agent: "agent", Execution: "run"}, adapter.ReadResult{
			AgentID: "agent",
			Events:  complete,
			Diagnostics: []adapter.Diagnostic{
				{Code: "unscoped_usage", Message: "agent level", Scope: "agent", AgentID: "agent", Metric: "usage"},
			},
		})
		if agent.Completeness != "partial" || len(agent.Diagnostics) != 1 || agent.Diagnostics[0].Scope != "agent" || agent.Diagnostics[0].ExecutionID != nil || agent.Target.ExecutionID == nil {
			t.Fatalf("agent diagnostic must not be rewritten as an execution or source finding: %+v", agent)
		}
		execution := Collect("codex", adapter.Request{Agent: "agent", Execution: "run"}, adapter.ReadResult{
			AgentID: "agent",
			Events: []adapter.Event{
				{ID: "start", Session: "agent", Execution: "run", Kind: "execution_start", Time: t0},
				{ID: "use", Session: "agent", Execution: "run", Kind: "usage", UsageID: "u", Provider: "openai", Model: "m", Input: p(2)},
			},
		})
		if execution.Completeness != "partial" || execution.Usage.SelfCompleteness != "partial" || *execution.Usage.Self[0].Tokens.Input != 2 || len(execution.Diagnostics) != 1 || execution.Diagnostics[0].Scope != "execution" || execution.Diagnostics[0].ExecutionID == nil || *execution.Diagnostics[0].ExecutionID != "run" || execution.Diagnostics[0].Metric != "elapsed" {
			t.Fatalf("execution boundary must stay scoped to the selected invocation: %+v", execution)
		}
	})
}
