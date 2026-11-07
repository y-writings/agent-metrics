package report

import (
	"fmt"
	"github.com/y-writings/agent-metrics/internal/adapter"
	"sort"
	"time"
)

func Build(product, parent, task string, events []adapter.Event, warnings []string) Report {
	r := Report{SchemaVersion: SchemaVersion, Product: product, Status: "unknown", Completeness: "complete"}
	r.Task.ParentID = parent
	r.Task.TaskID = task
	for _, w := range warnings {
		r.Diagnostics = append(r.Diagnostics, Diagnostic{"record_warning", w})
		r.Completeness = "partial"
	}
	children := map[string]string{}
	included := map[string]bool{parent: true}
	for changed := true; changed; {
		changed = false
		for _, e := range events {
			if e.Task != task {
				continue
			}
			if e.Parent != "" && included[e.Parent] && !included[e.Session] {
				included[e.Session] = true
				children[e.Session] = e.Parent
				changed = true
			}
		}
	}
	byAgent := map[string][]adapter.Event{}
	for _, e := range events {
		if !included[e.Session] || e.Task != task {
			continue
		}
		byAgent[e.Session] = append(byAgent[e.Session], e)
	}
	if len(byAgent) == 0 {
		r.Completeness = "incomplete"
		r.Diagnostics = append(r.Diagnostics, Diagnostic{"task_not_found", "no records matched the parent and task identifiers"})
		return r
	}
	var start, end *adapter.Event
	for _, e := range byAgent[parent] {
		x := e
		if (e.Kind == "task_start" || e.Kind == "turn_start") && (start == nil || e.Time.Before(start.Time)) {
			start = &x
		}
		if e.Kind == "task_end" || e.Kind == "turn_end" {
			if end == nil || e.Time.After(end.Time) {
				end = &x
			}
		}
	}
	r.Timing.StartDefinition = "parent task/turn start event"
	r.Timing.EndDefinition = "parent task/turn terminal event"
	if start != nil {
		if !start.Time.IsZero() {
			r.Timing.StartedAt = &start.Time
		}
		r.Timing.StartSourceID = &start.ID
	}
	if end != nil {
		if !end.Time.IsZero() {
			r.Timing.EndedAt = &end.Time
		}
		r.Timing.EndSourceID = &end.ID
		r.Status = firstStatus(end.Status, "completed")
	} else {
		r.Status = "in_progress"
	}
	if r.Timing.StartedAt == nil || r.Timing.EndedAt == nil {
		r.Completeness = "partial"
		r.Diagnostics = append(r.Diagnostics, Diagnostic{"missing_boundary", "parent start or terminal event or its timestamp is absent; last log time was not substituted"})
	} else if end.Time.Before(start.Time) {
		r.Completeness = "partial"
		r.Diagnostics = append(r.Diagnostics, Diagnostic{"invalid_boundary", "parent terminal event precedes its start; elapsed time is unknown"})
	} else {
		ms := end.Time.Sub(start.Time).Milliseconds()
		r.Timing.ElapsedMilliseconds = &ms
	}
	for id, es := range byAgent {
		a := Agent{ID: id, Status: "unknown"}
		if p, ok := children[id]; ok {
			a.ParentID = &p
		}
		var spawn *time.Time
		hasTerminal := false
		for _, e := range es {
			a.SourceIDs = append(a.SourceIDs, e.ID)
			if e.Kind == "spawn" && !e.Time.IsZero() {
				x := e.Time
				spawn = &x
			}
			if e.Status != "" {
				a.Status = e.Status
			}
			if e.Kind == "task_end" || e.Kind == "turn_end" {
				hasTerminal = true
			}
		}
		if id != parent && !hasTerminal {
			r.Completeness = "partial"
			r.Diagnostics = append(r.Diagnostics, Diagnostic{"missing_terminal", fmt.Sprintf("agent %s has no terminal event for the selected task", id)})
		}
		a.SpawnedAt = spawn
		a.Usage = aggregate(es, &r)
		r.Agents = append(r.Agents, a)
	}
	sort.Slice(r.Agents, func(i, j int) bool { return r.Agents[i].ID < r.Agents[j].ID })
	// Resolve snapshots and duplicate records within each agent before combining
	// independent agents' usage. A snapshot never supersedes another agent.
	sums := map[modelKey]Tokens{}
	for _, a := range r.Agents {
		for _, u := range a.Usage {
			k := modelKey{u.Provider, u.Model}
			t := sums[k]
			add(&t.Input, u.Tokens.Input)
			add(&t.CacheRead, u.Tokens.CacheRead)
			add(&t.CacheWrite, u.Tokens.CacheWrite)
			add(&t.Output, u.Tokens.Output)
			add(&t.Reasoning, u.Tokens.Reasoning)
			add(&t.Total, u.Tokens.Total)
			sums[k] = t
		}
	}
	r.Usage = sortedUsage(sums)
	return r
}
func firstStatus(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

type modelKey struct{ provider, model string }

func aggregate(es []adapter.Event, r *Report) []ModelUsage {
	sums := map[modelKey]Tokens{}
	seen := map[string]bool{}
	cumulative := map[modelKey]adapter.Event{}
	for _, e := range es {
		// Usage is independent of the source type: messages and lifecycle events
		// can carry counters without losing their original kind.
		if e.Kind != "usage" && e.Input == nil && e.CacheRead == nil && e.CacheWrite == nil && e.Output == nil && e.Reasoning == nil && e.Total == nil {
			continue
		}
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		k := modelKey{e.Provider, e.Model}
		if e.Model == "" {
			r.Completeness = "partial"
			r.Diagnostics = append(r.Diagnostics, Diagnostic{"unknown_model", fmt.Sprintf("usage event %s has no model", e.ID)})
			continue
		}
		if e.Cumulative {
			if previous, ok := cumulative[k]; !ok || !e.Time.Before(previous.Time) {
				cumulative[k] = e
			}
			continue
		}
		t := sums[k]
		add(&t.Input, e.Input)
		add(&t.CacheRead, e.CacheRead)
		add(&t.CacheWrite, e.CacheWrite)
		add(&t.Output, e.Output)
		add(&t.Reasoning, e.Reasoning)
		add(&t.Total, e.Total)
		sums[k] = t
	}
	// A cumulative snapshot supersedes increments for the same scope/model. This
	// prevents exports containing both representations from being double-counted.
	for k, e := range cumulative {
		t := Tokens{}
		add(&t.Input, e.Input)
		add(&t.CacheRead, e.CacheRead)
		add(&t.CacheWrite, e.CacheWrite)
		add(&t.Output, e.Output)
		add(&t.Reasoning, e.Reasoning)
		add(&t.Total, e.Total)
		sums[k] = t
	}
	return sortedUsage(sums)
}

func sortedUsage(sums map[modelKey]Tokens) []ModelUsage {
	var out []ModelUsage
	for k, t := range sums {
		out = append(out, ModelUsage{k.provider, k.model, t})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider == out[j].Provider {
			return out[i].Model < out[j].Model
		}
		return out[i].Provider < out[j].Provider
	})
	return out
}
func add(dst **int64, v *int64) {
	if v == nil {
		return
	}
	if *dst == nil {
		x := int64(0)
		*dst = &x
	}
	**dst += *v
}
