package report

import (
	"github.com/y-writings/agent-metrics/internal/adapter"
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
