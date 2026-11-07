package report

import (
	"github.com/y-writings/agent-metrics/internal/adapter"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func p(n int64) *int64 { return &n }
func TestBuildDescendantsTimingAndDeduplication(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	es := []adapter.Event{
		{ID: "s", Session: "root", Task: "turn-2", Kind: "turn_start", Time: t0},
		{ID: "old", Session: "root", Task: "turn-1", Kind: "usage", Model: "old", Input: p(99)},
		{ID: "u1", Session: "root", Task: "turn-2", Kind: "usage", Provider: "openai", Model: "gpt", Input: p(10), Output: p(2), Total: p(12)},
		{ID: "cspawn", Session: "child", Parent: "root", Task: "turn-2", Kind: "spawn", Time: t0.Add(time.Second)},
		{ID: "cu", Session: "child", Parent: "root", Task: "turn-2", Kind: "usage", Provider: "openai", Model: "gpt", Input: p(3), Output: p(1), Total: p(4)},
		{ID: "cu", Session: "child", Parent: "root", Task: "turn-2", Kind: "usage", Provider: "openai", Model: "gpt", Input: p(3)},
		{ID: "g", Session: "grand", Parent: "child", Task: "turn-2", Kind: "usage", Provider: "anthropic", Model: "claude", Input: p(5)},
		{ID: "e", Session: "root", Task: "turn-2", Kind: "turn_end", Time: t0.Add(10 * time.Second), Status: "completed"},
	}
	r := Build("codex", "root", "turn-2", es, nil)
	if r.Timing.ElapsedMilliseconds == nil || *r.Timing.ElapsedMilliseconds != 10000 {
		t.Fatalf("timing: %#v", r.Timing)
	}
	if len(r.Agents) != 3 {
		t.Fatalf("agents=%d", len(r.Agents))
	}
	if len(r.Usage) != 2 {
		t.Fatalf("usage=%#v", r.Usage)
	}
	if *r.Usage[1].Tokens.Total != 16 {
		t.Fatalf("total=%v", r.Usage[1].Tokens.Total)
	}
}
func TestMissingEndIsPartial(t *testing.T) {
	r := Build("claude-code", "p", "t", []adapter.Event{{ID: "s", Session: "p", Task: "t", Kind: "task_start"}}, nil)
	if r.Completeness != "partial" || r.Timing.EndedAt != nil {
		t.Fatalf("%#v", r)
	}
}

func TestCumulativeUsageAcrossAgents(t *testing.T) {
	r := Build("codex", "root", "task", []adapter.Event{
		{ID: "root-snapshot", Session: "root", Task: "task", Kind: "usage", Provider: "provider", Model: "model", Input: p(10), Cumulative: true},
		{ID: "child-snapshot", Session: "child", Parent: "root", Task: "task", Kind: "usage", Provider: "provider", Model: "model", Input: p(3), Cumulative: true},
	}, nil)
	if len(r.Usage) != 1 || r.Usage[0].Tokens.Input == nil || *r.Usage[0].Tokens.Input != 13 {
		t.Fatalf("parent and child snapshots must sum to 13: %+v", r.Usage)
	}
	for _, a := range r.Agents {
		want := int64(10)
		if a.ID == "child" {
			want = 3
		}
		if len(a.Usage) != 1 || a.Usage[0].Tokens.Input == nil || *a.Usage[0].Tokens.Input != want {
			t.Errorf("agent %s must retain its own snapshot %d: %+v", a.ID, want, a.Usage)
		}
	}
}

func TestLatestCumulativeSnapshotSupersedesDeltas(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	older := adapter.Event{ID: "older", Session: "root", Task: "task", Kind: "usage", Model: "model", Input: p(4), Cumulative: true, Time: t0}
	newer := adapter.Event{ID: "newer", Session: "root", Task: "task", Kind: "usage", Model: "model", Input: p(10), Cumulative: true, Time: t0.Add(time.Second)}
	for _, tc := range []struct {
		name      string
		snapshots []adapter.Event
	}{
		{"chronological", []adapter.Event{older, newer}},
		{"reversed_file_order", []adapter.Event{newer, older}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			es := append([]adapter.Event{
				{ID: "delta", Session: "root", Task: "task", Kind: "usage", Model: "model", Input: p(2)},
			}, tc.snapshots...)
			r := Build("codex", "root", "task", es, nil)
			if len(r.Usage) != 1 || r.Usage[0].Tokens.Input == nil || *r.Usage[0].Tokens.Input != 10 {
				t.Fatalf("latest snapshot is 10 regardless of file order; deltas are not added: %+v", r.Usage)
			}
		})
	}
}

func TestUsageDeduplicationIsAgentScoped(t *testing.T) {
	r := Build("codex", "root", "task", []adapter.Event{
		{ID: "usage", Session: "root", Task: "task", Kind: "usage", Model: "model", Input: p(2)},
		{ID: "usage", Session: "root", Task: "task", Kind: "usage", Model: "model", Input: p(2)},
		{ID: "usage", Session: "child", Parent: "root", Task: "task", Kind: "usage", Model: "model", Input: p(3)},
	}, nil)
	if len(r.Usage) != 1 || r.Usage[0].Tokens.Input == nil || *r.Usage[0].Tokens.Input != 5 {
		t.Fatalf("deduplicate within each agent, not across independent agents: %+v", r.Usage)
	}
}

func TestDatedSnapshotTakesPrecedenceOverUndated(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := Build("codex", "root", "task", []adapter.Event{
		{ID: "dated", Session: "root", Task: "task", Kind: "usage", Model: "model", Input: p(10), Cumulative: true, Time: t0},
		{ID: "undated", Session: "root", Task: "task", Kind: "usage", Model: "model", Input: p(20), Cumulative: true},
	}, nil)
	if len(r.Usage) != 1 || r.Usage[0].Tokens.Input == nil || *r.Usage[0].Tokens.Input != 10 {
		t.Fatalf("an undated snapshot cannot supersede a dated snapshot: %+v", r.Usage)
	}
}

func TestInvalidParentTimingIsPartial(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		start, end time.Time
		omitStart  bool
	}{
		{name: "missing_timestamps"},
		{name: "missing_start_timestamp", end: t0},
		{name: "missing_end_timestamp", start: t0},
		{name: "reversed", start: t0.Add(time.Second), end: t0},
		{name: "missing_start_event", end: t0, omitStart: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			es := []adapter.Event{{ID: "end", Session: "root", Task: "task", Kind: "task_end", Time: tc.end, Status: "failed"}}
			if !tc.omitStart {
				es = append(es, adapter.Event{ID: "start", Session: "root", Task: "task", Kind: "task_start", Time: tc.start})
			}
			r := Build("codex", "root", "task", es, nil)
			if r.Completeness != "partial" || r.Timing.ElapsedMilliseconds != nil || len(r.Diagnostics) == 0 {
				t.Fatalf("invalid parent timing must be null and diagnosed as partial: %+v", r)
			}
			if tc.start.IsZero() && r.Timing.StartedAt != nil || tc.end.IsZero() && r.Timing.EndedAt != nil {
				t.Fatalf("missing timestamps must not become year-0001 values: %+v", r.Timing)
			}
			if r.Status != "failed" {
				t.Fatalf("terminal status must survive missing timing: got %s, want failed", r.Status)
			}
		})
	}
}

func TestTypedMessageUsageReachesReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	data := `{"id":"start","session_id":"root","task_id":"task","type":"task_start","timestamp":"2026-01-01T00:00:00Z"}
{"id":"message","session_id":"root","task_id":"task","type":"assistant","message":{"model":"model","usage":{"input_tokens":10,"output_tokens":2}}}
{"id":"end","session_id":"root","task_id":"task","type":"task_end","timestamp":"2026-01-01T00:00:01Z"}
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := adapter.New("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	events, warnings, err := a.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	r := Build(a.Name(), "root", "task", events, warnings)
	if len(r.Usage) != 1 || r.Usage[0].Tokens.Input == nil || *r.Usage[0].Tokens.Input != 10 || r.Usage[0].Tokens.Output == nil || *r.Usage[0].Tokens.Output != 2 {
		t.Fatalf("typed message's observed usage must reach the report: %+v", r.Usage)
	}
}

func TestUsageCountersDoNotRequireUsageKind(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event adapter.Event
		want  Tokens
	}{
		{"cache_read_zero", adapter.Event{CacheRead: p(0)}, Tokens{CacheRead: p(0)}},
		{"cache_write", adapter.Event{CacheWrite: p(2)}, Tokens{CacheWrite: p(2)}},
		{"reasoning", adapter.Event{Reasoning: p(3)}, Tokens{Reasoning: p(3)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := tc.event
			e.ID, e.Session, e.Task, e.Kind, e.Model = "message", "root", "task", "assistant", "model"
			r := Build("claude-code", "root", "task", []adapter.Event{e}, nil)
			if len(r.Usage) != 1 || !reflect.DeepEqual(r.Usage[0].Tokens, tc.want) {
				t.Fatalf("observed counter must be retained and unobserved fields stay nil: got %+v, want %+v", r.Usage, tc.want)
			}
		})
	}
}

func TestBoundaryUsageRetainsTiming(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := Build("codex", "root", "task", []adapter.Event{
		{ID: "start", Session: "root", Task: "task", Kind: "task_start", Time: t0},
		{ID: "end", Session: "root", Task: "task", Kind: "task_end", Time: t0.Add(time.Second), Model: "model", Input: p(4)},
	}, nil)
	if len(r.Usage) != 1 || r.Usage[0].Tokens.Input == nil || *r.Usage[0].Tokens.Input != 4 {
		t.Fatalf("terminal event's usage must be counted: %+v", r.Usage)
	}
	if r.Completeness != "complete" || r.Timing.ElapsedMilliseconds == nil || *r.Timing.ElapsedMilliseconds != 1000 {
		t.Fatalf("usage must not replace terminal lifecycle semantics: %+v", r)
	}
}

func TestDescendantDiscoveryExcludesHistoricalParent(t *testing.T) {
	r := Build("codex", "root", "selected", []adapter.Event{
		{ID: "root-usage", Session: "root", Task: "selected", Kind: "usage", Model: "model", Input: p(2)},
		{ID: "old-link", Session: "reused", Parent: "root", Task: "old", Kind: "spawn"},
		{ID: "reused-usage", Session: "reused", Parent: "other-root", Task: "selected", Kind: "usage", Model: "model", Input: p(99)},
		{ID: "grand-usage", Session: "grand", Parent: "child", Task: "selected", Kind: "usage", Model: "model", Input: p(5)},
		{ID: "child-usage", Session: "child", Parent: "root", Task: "selected", Kind: "usage", Model: "model", Input: p(3)},
	}, nil)
	if len(r.Agents) != 3 || r.Agents[0].ID != "child" || r.Agents[1].ID != "grand" || r.Agents[2].ID != "root" {
		t.Fatalf("historical parent links must not include the reused child: %+v", r.Agents)
	}
	if r.Agents[1].ParentID == nil || *r.Agents[1].ParentID != "child" {
		t.Fatalf("current-task recursion must retain the grandchild's parent: %+v", r.Agents[1])
	}
	if len(r.Usage) != 1 || r.Usage[0].Tokens.Input == nil || *r.Usage[0].Tokens.Input != 10 {
		t.Fatalf("unrelated child's 99 tokens must not contribute: %+v", r.Usage)
	}
}

func TestDescendantTerminalControlsCompleteness(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, terminal := range []string{"missing", "task_end", "turn_end"} {
		t.Run(terminal, func(t *testing.T) {
			es := []adapter.Event{
				{ID: "start", Session: "root", Task: "task", Kind: "task_start", Time: t0},
				{ID: "spawn", Session: "child", Parent: "root", Task: "task", Kind: "spawn", Time: t0},
				{ID: "old-end", Session: "child", Task: "old", Kind: "task_end", Time: t0},
				{ID: "end", Session: "root", Task: "task", Kind: "task_end", Time: t0.Add(time.Second)},
			}
			want := "partial"
			if terminal != "missing" {
				es = append(es, adapter.Event{ID: "child-end", Session: "child", Task: "task", Kind: terminal, Status: "failed", Time: t0.Add(time.Second)})
				want = "complete"
			}
			r := Build("codex", "root", "task", es, nil)
			if r.Completeness != want {
				t.Fatalf("child terminal determines completeness: got %s, want %s", r.Completeness, want)
			}
			if terminal == "missing" {
				if len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != "missing_terminal" || !strings.Contains(r.Diagnostics[0].Message, "child") {
					t.Fatalf("diagnostic must identify unterminated child: %+v", r.Diagnostics)
				}
			} else if len(r.Diagnostics) != 0 {
				t.Fatalf("explicit failure terminal still makes data complete: %+v", r.Diagnostics)
			}
			if r.Status != "completed" || r.Timing.ElapsedMilliseconds == nil || *r.Timing.ElapsedMilliseconds != 1000 {
				t.Fatalf("child log completeness must not change observed parent execution: %+v", r)
			}
		})
	}
}
