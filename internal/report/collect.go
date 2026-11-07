package report

import (
	"fmt"
	"sort"
	"time"

	"github.com/y-writings/agent-metrics/internal/adapter"
)

const ExecutionSchemaVersion = "2.0"

type Target struct {
	Product     string  `json:"product"`
	AgentID     string  `json:"agent_id"`
	ExecutionID *string `json:"execution_id"`
	SessionID   *string `json:"session_id"`
}

type UsageScopes struct {
	Self    []ModelUsage `json:"self"`
	Subtree []ModelUsage `json:"subtree"`
}

type Execution struct {
	ID                  string       `json:"id"`
	AgentID             string       `json:"agent_id"`
	ParentExecutionID   *string      `json:"parent_execution_id"`
	SpawnedAt           *time.Time   `json:"spawned_at"`
	StartedAt           *time.Time   `json:"started_at"`
	EndedAt             *time.Time   `json:"ended_at"`
	ElapsedMilliseconds *int64       `json:"elapsed_milliseconds"`
	Status              string       `json:"status"`
	Usage               []ModelUsage `json:"usage"`
	SourceIDs           []string     `json:"source_ids"`
}

type ExecutionReport struct {
	SchemaVersion string       `json:"schema_version"`
	Target        Target       `json:"target"`
	Status        string       `json:"status"`
	Completeness  string       `json:"completeness"`
	Timing        Timing       `json:"timing"`
	Usage         UsageScopes  `json:"usage"`
	Executions    []Execution  `json:"executions"`
	Diagnostics   []Diagnostic `json:"diagnostics"`
}

type executionKey struct{ id, agent string }

// Collect resolves exactly one invocation of agent and follows explicit
// invocation links. It never infers relationships from timestamps or cwd.
func Collect(product, agent, requestedExecution string, events []adapter.Event, warnings []string) ExecutionReport {
	r := ExecutionReport{SchemaVersion: ExecutionSchemaVersion, Target: Target{Product: product, AgentID: agent}, Status: "unknown", Completeness: "complete"}
	for _, w := range warnings {
		r.partial("record_warning", w)
	}
	candidates := map[string]bool{}
	keysByID := map[string]map[executionKey]bool{}
	for _, e := range events {
		if e.Execution != "" {
			if keysByID[e.Execution] == nil {
				keysByID[e.Execution] = map[executionKey]bool{}
			}
			keysByID[e.Execution][executionKey{e.Execution, e.Session}] = true
		}
		if e.Session == agent && e.Execution != "" {
			candidates[e.Execution] = true
		}
	}
	selected := requestedExecution
	if selected == "" {
		if len(candidates) == 1 {
			for id := range candidates {
				selected = id
			}
		} else if len(candidates) == 0 {
			r.Completeness = "incomplete"
			r.Diagnostics = append(r.Diagnostics, Diagnostic{"execution_not_found", "no execution was found for the specified agent"})
			return r
		} else {
			r.Completeness = "incomplete"
			ids := keys(candidates)
			r.Diagnostics = append(r.Diagnostics, Diagnostic{"ambiguous_execution", fmt.Sprintf("agent has multiple executions; select one with --execution: %v", ids)})
			return r
		}
	} else if !candidates[selected] {
		r.Completeness = "incomplete"
		r.Diagnostics = append(r.Diagnostics, Diagnostic{"execution_not_found", "the selected execution does not belong to the specified agent"})
		return r
	}
	r.Target.ExecutionID = &selected
	included := map[executionKey]bool{{selected, agent}: true}
	ambiguous := map[string]bool{}
	unique := func(id string) (executionKey, bool) {
		if len(keysByID[id]) != 1 {
			return executionKey{id: id}, false
		}
		for key := range keysByID[id] {
			return key, true
		}
		return executionKey{}, false
	}
	for changed := true; changed; {
		changed = false
		for _, e := range events {
			if included[executionKey{e.Execution, e.Session}] && e.ReferencedExecution != "" {
				child, ok := unique(e.ReferencedExecution)
				if !ok && len(keysByID[e.ReferencedExecution]) > 1 {
					ambiguous[e.ReferencedExecution] = true
				}
				if !ok && len(keysByID[e.ReferencedExecution]) == 0 {
					child = executionKey{id: e.ReferencedExecution}
				}
				if (ok || len(keysByID[e.ReferencedExecution]) == 0) && !included[child] {
					included[child] = true
					changed = true
				}
			}
			if e.Execution != "" && e.ParentExecution != "" {
				parent, ok := unique(e.ParentExecution)
				child := executionKey{e.Execution, e.Session}
				if !ok {
					for candidate := range keysByID[e.ParentExecution] {
						if included[candidate] {
							ambiguous[e.ParentExecution] = true
						}
					}
				}
				if ok && included[parent] && !included[child] {
					included[child] = true
					changed = true
				}
			}
		}
	}
	for id := range ambiguous {
		r.partial("ambiguous_execution_link", fmt.Sprintf("execution %s is shared by multiple agents; its delegation links cannot be resolved", id))
	}
	by := map[executionKey][]adapter.Event{}
	for _, e := range events {
		key := executionKey{e.Execution, e.Session}
		if included[key] {
			by[key] = append(by[key], e)
		}
	}
	spawns := map[executionKey]adapter.Event{}
	for _, e := range events {
		if e.Kind != "spawn" || e.ReferencedExecution == "" {
			continue
		}
		child, ok := unique(e.ReferencedExecution)
		if !ok || !included[child] {
			continue
		}
		if previous, exists := spawns[child]; !exists || (!e.Time.IsZero() && (previous.Time.IsZero() || e.Time.Before(previous.Time))) {
			spawns[child] = e
		}
	}
	for key := range included {
		es := by[key]
		if len(es) == 0 {
			r.partial("missing_descendant", fmt.Sprintf("execution %s is referenced but its records are missing", key.id))
			continue
		}
		x, start, end := executionFrom(key.id, es, &r)
		if spawn, ok := spawns[key]; ok {
			if !spawn.Time.IsZero() {
				v := spawn.Time
				x.SpawnedAt = &v
			}
			if x.ParentExecutionID == nil && spawn.Execution != "" {
				v := spawn.Execution
				x.ParentExecutionID = &v
			}
		}
		r.Executions = append(r.Executions, x)
		if key == (executionKey{selected, agent}) {
			r.Target.SessionID = &x.AgentID
			r.Status = x.Status
			r.Timing = Timing{StartedAt: x.StartedAt, EndedAt: x.EndedAt, ElapsedMilliseconds: x.ElapsedMilliseconds, StartDefinition: "explicit execution start event", EndDefinition: "explicit execution terminal event"}
			if start != nil {
				r.Timing.StartSourceID = &start.ID
			}
			if end != nil {
				r.Timing.EndSourceID = &end.ID
			}
			r.Usage.Self = x.Usage
		}
	}
	sort.Slice(r.Executions, func(i, j int) bool {
		if r.Executions[i].ID == r.Executions[j].ID {
			return r.Executions[i].AgentID < r.Executions[j].AgentID
		}
		return r.Executions[i].ID < r.Executions[j].ID
	})
	sums := map[modelKey]Tokens{}
	for _, x := range r.Executions {
		for _, u := range x.Usage {
			t := sums[modelKey{u.Provider, u.Model}]
			addTokens(&t, u.Tokens)
			sums[modelKey{u.Provider, u.Model}] = t
		}
	}
	r.Usage.Subtree = sortedUsage(sums)
	return r
}

func executionFrom(id string, es []adapter.Event, r *ExecutionReport) (Execution, *adapter.Event, *adapter.Event) {
	x := Execution{ID: id, AgentID: es[0].Session, Status: "in_progress"}
	var start, end *adapter.Event
	for i := range es {
		e := &es[i]
		x.SourceIDs = append(x.SourceIDs, e.ID)
		if e.ParentExecution != "" {
			v := e.ParentExecution
			x.ParentExecutionID = &v
		}
		if e.Kind == "spawn" && e.ReferencedExecution == "" && !e.Time.IsZero() {
			v := e.Time
			x.SpawnedAt = &v
		}
		if (e.Kind == "task_start" || e.Kind == "execution_start") && (start == nil || (start.Time.IsZero() && !e.Time.IsZero()) || (!e.Time.IsZero() && e.Time.Before(start.Time))) {
			start = e
		}
		if (e.Kind == "task_end" || e.Kind == "execution_end") && (end == nil || (end.Time.IsZero() && !e.Time.IsZero()) || (!e.Time.IsZero() && e.Time.After(end.Time))) {
			end = e
		}
	}
	legacy := Report{Completeness: "complete"}
	x.Usage = aggregate(es, &legacy)
	for _, d := range legacy.Diagnostics {
		r.partial(d.Code, d.Message)
	}
	if len(x.Usage) == 0 {
		r.partial("unknown_usage", fmt.Sprintf("execution %s has no observed usage counters", id))
	}
	if start != nil && !start.Time.IsZero() {
		v := start.Time
		x.StartedAt = &v
	}
	if end != nil {
		x.Status = firstStatus(end.Status, "completed")
		if !end.Time.IsZero() {
			v := end.Time
			x.EndedAt = &v
		}
	}
	if x.StartedAt == nil || x.EndedAt == nil {
		r.partial("missing_boundary", fmt.Sprintf("execution %s lacks an explicit timestamped start or terminal event", id))
	} else if x.EndedAt.Before(*x.StartedAt) {
		x.ElapsedMilliseconds = nil
		r.partial("invalid_boundary", fmt.Sprintf("execution %s ends before it starts", id))
	} else {
		v := x.EndedAt.Sub(*x.StartedAt).Milliseconds()
		x.ElapsedMilliseconds = &v
	}
	return x, start, end
}
func (r *ExecutionReport) partial(code, msg string) {
	if r.Completeness == "complete" {
		r.Completeness = "partial"
	}
	r.Diagnostics = append(r.Diagnostics, Diagnostic{code, msg})
}
func keys(m map[string]bool) []string {
	v := make([]string, 0, len(m))
	for k := range m {
		v = append(v, k)
	}
	sort.Strings(v)
	return v
}
func addTokens(a *Tokens, b Tokens) {
	add(&a.Input, b.Input)
	add(&a.CacheRead, b.CacheRead)
	add(&a.CacheWrite, b.CacheWrite)
	add(&a.Output, b.Output)
	add(&a.Reasoning, b.Reasoning)
	add(&a.Total, b.Total)
}
