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
			if e.Task != task && e.Session == parent {
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
		r.Timing.StartedAt = &start.Time
		r.Timing.StartSourceID = &start.ID
	}
	if end != nil {
		r.Timing.EndedAt = &end.Time
		r.Timing.EndSourceID = &end.ID
	}
	if start != nil && end != nil {
		ms := end.Time.Sub(start.Time).Milliseconds()
		r.Timing.ElapsedMilliseconds = &ms
		r.Status = firstStatus(end.Status, "completed")
	} else {
		r.Completeness = "partial"
		r.Status = "in_progress"
		r.Diagnostics = append(r.Diagnostics, Diagnostic{"missing_boundary", "parent start or terminal event is absent; last log time was not substituted"})
	}
	for id, es := range byAgent {
		a := Agent{ID: id, Status: "unknown"}
		if p, ok := children[id]; ok {
			a.ParentID = &p
		}
		var spawn *time.Time
		for _, e := range es {
			a.SourceIDs = append(a.SourceIDs, e.ID)
			if e.Kind == "spawn" && !e.Time.IsZero() {
				x := e.Time
				spawn = &x
			}
			if e.Status != "" {
				a.Status = e.Status
			}
		}
		a.SpawnedAt = spawn
		a.Usage = aggregate(es, &r)
		r.Agents = append(r.Agents, a)
	}
	sort.Slice(r.Agents, func(i, j int) bool { return r.Agents[i].ID < r.Agents[j].ID })
	var all []adapter.Event
	for _, es := range byAgent {
		all = append(all, es...)
	}
	r.Usage = aggregate(all, &r)
	return r
}
func firstStatus(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func aggregate(es []adapter.Event, r *Report) []ModelUsage {
	type key struct{ p, m string }
	sums := map[key]Tokens{}
	seen := map[string]bool{}
	cumulative := map[key]adapter.Event{}
	for _, e := range es {
		if e.Kind != "usage" || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		k := key{e.Provider, e.Model}
		if e.Model == "" {
			r.Completeness = "partial"
			r.Diagnostics = append(r.Diagnostics, Diagnostic{"unknown_model", fmt.Sprintf("usage event %s has no model", e.ID)})
			continue
		}
		if e.Cumulative {
			cumulative[k] = e
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
	var out []ModelUsage
	for k, t := range sums {
		out = append(out, ModelUsage{k.p, k.m, t})
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
