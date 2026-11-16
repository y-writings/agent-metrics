package report

import (
	"fmt"
	"sort"

	"github.com/y-writings/agent-metrics/internal/adapter"
)

type executionKey struct{ id, agent string }

// Collect selects one invocation of the requested agent and the explicit
// descendants of that invocation. Relationships come only from execution links
// and delegations, never from timestamps or working directories.
func Collect(product string, request adapter.Request, result adapter.ReadResult) ExecutionReport {
	report := newReport(product, request.Agent)
	native := result.AgentID
	if native == "" {
		native = request.Agent
	}
	keysByID := map[string]map[executionKey]bool{}
	candidates := map[string]bool{}
	agentEvents := false
	eventExec := map[string]executionKey{}
	for _, event := range result.Events {
		if event.Session == native {
			agentEvents = true
		}
		if event.Execution == "" {
			continue
		}
		if keysByID[event.Execution] == nil {
			keysByID[event.Execution] = map[executionKey]bool{}
		}
		key := executionKey{event.Execution, event.Session}
		keysByID[event.Execution][key] = true
		if event.ID != "" {
			eventExec[event.ID] = key
		}
		if event.Session == native {
			candidates[event.Execution] = true
		}
	}
	selected := request.Execution
	switch {
	case selected == "" && len(candidates) == 1:
		for id := range candidates {
			selected = id
		}
	case selected == "" && len(candidates) == 0 && agentEvents:
		return retainUnscoped(report, native, result)
	case selected == "" && len(candidates) == 0:
		report.Completeness = "incomplete"
		report.Diagnostics = append(report.Diagnostics, relevantDiagnostics(result.Diagnostics, native, nil)...)
		if !hasResolutionFailure(report.Diagnostics) {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "execution_not_found", Message: "no execution was found for the specified agent", Scope: "agent", AgentID: native})
		}
		return report
	case selected == "" && len(candidates) > 1:
		report.Completeness = "incomplete"
		report.Diagnostics = append(report.Diagnostics, relevantDiagnostics(result.Diagnostics, native, nil)...)
		ids := mapKeys(candidates)
		report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "ambiguous_execution", Message: fmt.Sprintf("agent has multiple executions; select one with --execution: %v", ids), Scope: "agent", AgentID: native})
		return report
	case selected != "" && !candidates[selected]:
		report.Completeness = "incomplete"
		report.Diagnostics = append(report.Diagnostics, relevantDiagnostics(result.Diagnostics, native, nil)...)
		report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "execution_not_found", Message: "the selected execution does not belong to the specified agent", Scope: "agent", AgentID: native})
		return report
	}
	report.Target.ExecutionID = &selected

	included := map[executionKey]bool{{selected, native}: true}
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
		for _, event := range result.Events {
			if included[executionKey{event.Execution, event.Session}] && event.ReferencedExecution != "" {
				child, ok := unique(event.ReferencedExecution)
				if !ok && len(keysByID[event.ReferencedExecution]) > 1 {
					ambiguous[event.ReferencedExecution] = true
				}
				if !ok && len(keysByID[event.ReferencedExecution]) == 0 {
					child = executionKey{id: event.ReferencedExecution}
				}
				if (ok || len(keysByID[event.ReferencedExecution]) == 0) && !included[child] {
					included[child] = true
					changed = true
				}
			}
			if event.Execution != "" && event.ParentExecution != "" {
				parent, ok := unique(event.ParentExecution)
				child := executionKey{event.Execution, event.Session}
				if !ok {
					for candidate := range keysByID[event.ParentExecution] {
						if included[candidate] {
							ambiguous[event.ParentExecution] = true
						}
					}
				}
				if ok && included[parent] && !included[child] {
					included[child] = true
					changed = true
				}
			}
		}
		for _, delegation := range result.Delegations {
			if _, ok := delegationParent(delegation, included, eventExec); !ok || delegation.Child == nil || delegation.Child.ExecutionID == "" {
				continue
			}
			child, ok := childKey(delegation, keysByID, ambiguous)
			if ok && !included[child] {
				included[child] = true
				changed = true
			}
		}
	}
	for _, diagnostic := range relevantDiagnostics(result.Diagnostics, native, included) {
		report.add(diagnostic)
	}
	for id := range ambiguous {
		report.add(Diagnostic{Code: "ambiguous_execution_link", Message: fmt.Sprintf("execution %s is shared by multiple agents; its delegation links cannot be resolved", id), Scope: "execution", AgentID: native, ExecutionID: &selected, Metric: "subtree"})
	}
	type missingChild struct{ agent, source string }
	var missing []missingChild
	seenMissing := map[string]bool{}
	for _, delegation := range result.Delegations {
		if _, ok := delegationParent(delegation, included, eventExec); !ok {
			continue
		}
		if delegation.Child != nil && delegation.Child.ExecutionID != "" {
			continue
		}
		if delegation.ChildAgentID == "" || seenMissing[delegation.ChildAgentID] {
			continue
		}
		seenMissing[delegation.ChildAgentID] = true
		missing = append(missing, missingChild{delegation.ChildAgentID, delegation.SourceID})
	}

	includedAgents := map[string]bool{}
	for key := range included {
		if key.agent != "" {
			includedAgents[key.agent] = true
		}
	}
	for _, event := range result.Events {
		if event.Execution != "" || !includedAgents[event.Session] {
			continue
		}
		if event.Kind == "usage" || hasUsageCounters(event) {
			report.UnattributedUsage = append(report.UnattributedUsage, unattributed(event))
			report.add(Diagnostic{Code: "unscoped_usage", Message: fmt.Sprintf("usage record %s for agent %s has no execution or task/turn ID", event.ID, event.Session), Scope: "agent", AgentID: event.Session, Metric: "usage", SourceID: event.ID})
		}
	}

	byKey := map[executionKey][]adapter.Event{}
	for _, event := range result.Events {
		key := executionKey{event.Execution, event.Session}
		if included[key] {
			byKey[key] = append(byKey[key], event)
		}
	}
	spawns := map[executionKey]adapter.Event{}
	for _, event := range result.Events {
		if event.Kind != "spawn" || event.ReferencedExecution == "" {
			continue
		}
		child, ok := unique(event.ReferencedExecution)
		if !ok || !included[child] {
			continue
		}
		if previous, exists := spawns[child]; !exists || (!event.Time.IsZero() && (previous.Time.IsZero() || event.Time.Before(previous.Time))) {
			spawns[child] = event
		}
	}
	parents := map[executionKey]string{}
	for _, delegation := range result.Delegations {
		parent, ok := delegationParent(delegation, included, eventExec)
		if !ok || delegation.Child == nil || delegation.Child.ExecutionID == "" {
			continue
		}
		child, ok := childKey(delegation, keysByID, map[string]bool{})
		if ok {
			parents[child] = parent.id
		}
	}
	usageByKey := map[executionKey]string{}
	for key := range included {
		events := byKey[key]
		if len(events) == 0 {
			message := fmt.Sprintf("execution %s is referenced but its records are missing", key.id)
			if key.agent != "" {
				message = fmt.Sprintf("execution %s for agent %s is referenced but its records are missing", key.id, key.agent)
			}
			report.add(Diagnostic{Code: "missing_descendant", Message: message, Scope: "execution", AgentID: native, RelatedAgentID: key.agent, ExecutionID: &selected, Metric: "subtree", SourceID: key.id})
			usageByKey[key] = "incomplete"
			continue
		}
		execution, usageCompleteness := executionFrom(key, events, &report)
		if spawn, ok := spawns[key]; ok {
			if !spawn.Time.IsZero() {
				value := spawn.Time
				execution.SpawnedAt = &value
			}
			if execution.ParentExecutionID == nil && spawn.Execution != "" {
				value := spawn.Execution
				execution.ParentExecutionID = &value
			}
		}
		if execution.ParentExecutionID == nil && parents[key] != "" {
			value := parents[key]
			execution.ParentExecutionID = &value
		}
		if includedAgents[key.agent] && agentHasUnscoped(result.Events, key.agent) {
			usageCompleteness = worse(usageCompleteness, "partial")
		}
		execution.UsageCompleteness = usageCompleteness
		usageByKey[key] = usageCompleteness
		report.Executions = append(report.Executions, execution)
	}
	for _, child := range missing {
		report.add(Diagnostic{Code: "missing_descendant", Message: fmt.Sprintf("agent %s is referenced but its execution was not resolved", child.agent), Scope: "execution", AgentID: native, RelatedAgentID: child.agent, ExecutionID: &selected, Metric: "subtree", SourceID: child.source})
	}
	sort.Slice(report.Executions, func(i, j int) bool {
		if report.Executions[i].ID == report.Executions[j].ID {
			return report.Executions[i].AgentID < report.Executions[j].AgentID
		}
		return report.Executions[i].ID < report.Executions[j].ID
	})
	targetKey := executionKey{selected, native}
	for i := range report.Executions {
		execution := &report.Executions[i]
		if execution.ID == selected && execution.AgentID == native {
			report.Status = execution.Status
			report.Timing = Timing{
				StartedAt:           execution.StartedAt,
				EndedAt:             execution.EndedAt,
				ElapsedMilliseconds: execution.ElapsedMilliseconds,
				StartDefinition:     definitionFrom(byKey[targetKey], true),
				EndDefinition:       definitionFrom(byKey[targetKey], false),
				StartSourceID:       sourceID(byKey[targetKey], true),
				EndSourceID:         sourceID(byKey[targetKey], false),
			}
			report.Usage.Self = append([]ModelUsage(nil), execution.Usage...)
			report.Usage.SelfCompleteness = execution.UsageCompleteness
			for _, event := range byKey[targetKey] {
				if event.SourceSession != "" {
					value := event.SourceSession
					report.Target.SessionID = &value
					break
				}
			}
		}
	}
	if report.Target.SessionID == nil && result.AgentID != "" && result.AgentID != request.Agent {
		value := result.AgentID
		report.Target.SessionID = &value
	}
	grouped := map[modelKey][]Tokens{}
	var sumOrder []modelKey
	for _, execution := range report.Executions {
		for _, usage := range execution.Usage {
			key := modelKey{deref(usage.Provider), deref(usage.Model)}
			if _, ok := grouped[key]; !ok {
				sumOrder = append(sumOrder, key)
			}
			grouped[key] = append(grouped[key], usage.Tokens)
		}
	}
	unknownSubtree := false
	for _, key := range sumOrder {
		tokens, partials := foldUsageTokens(grouped[key])
		if len(partials) > 0 {
			unknownSubtree = true
		}
		for _, metric := range partials {
			label := key.model
			if key.provider != "" && key.model != "" {
				label = key.provider + "/" + key.model
			}
			report.add(Diagnostic{
				Code: "unknown_counter", Message: fmt.Sprintf("subtree usage for %s is missing %s on some executions", label, metric),
				Scope: "execution", AgentID: native, ExecutionID: &selected, Metric: metric,
			})
		}
		report.Usage.Subtree = append(report.Usage.Subtree, ModelUsage{Provider: strPtr(key.provider), Model: strPtr(key.model), Tokens: tokens})
	}
	sort.Slice(report.Usage.Subtree, func(i, j int) bool {
		return modelLess(report.Usage.Subtree[i], report.Usage.Subtree[j])
	})
	sort.Slice(report.Usage.Self, func(i, j int) bool {
		return modelLess(report.Usage.Self[i], report.Usage.Self[j])
	})
	report.Usage.SubtreeCompleteness = subtreeCompleteness(usageByKey, len(missing) > 0 || len(ambiguous) > 0, report.Usage.Subtree)
	if unknownSubtree {
		report.Usage.SubtreeCompleteness = worse(report.Usage.SubtreeCompleteness, "partial")
	}
	if report.Completeness == "complete" && (report.Usage.SelfCompleteness != "complete" || report.Usage.SubtreeCompleteness != "complete") {
		report.Completeness = "partial"
	}
	return report
}

func newReport(product, agent string) ExecutionReport {
	return ExecutionReport{
		SchemaVersion: ExecutionSchemaVersion,
		Target:        Target{Product: product, AgentID: agent},
		Status:        "unknown",
		Completeness:  "complete",
		Usage: UsageScopes{
			Self:                []ModelUsage{},
			Subtree:             []ModelUsage{},
			SelfCompleteness:    "incomplete",
			SubtreeCompleteness: "incomplete",
		},
		Executions:        []Execution{},
		UnattributedUsage: []UnattributedUsage{},
		Diagnostics:       []Diagnostic{},
	}
}

func retainUnscoped(report ExecutionReport, native string, result adapter.ReadResult) ExecutionReport {
	report.Completeness = "partial"
	report.Diagnostics = append(report.Diagnostics, relevantDiagnostics(result.Diagnostics, native, nil)...)
	kept := false
	for _, event := range result.Events {
		if event.Session != native || event.Execution != "" {
			continue
		}
		if event.Kind == "usage" || hasUsageCounters(event) {
			report.UnattributedUsage = append(report.UnattributedUsage, unattributed(event))
			kept = true
		}
	}
	report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "missing_execution_id", Message: "records for the agent do not identify an execution", Scope: "agent", AgentID: native, Metric: "usage"})
	if !kept {
		report.Completeness = "incomplete"
	}
	if report.Target.SessionID == nil && result.AgentID != "" && result.AgentID != report.Target.AgentID {
		value := result.AgentID
		report.Target.SessionID = &value
	}
	return report
}

func executionFrom(key executionKey, events []adapter.Event, report *ExecutionReport) (Execution, string) {
	execution := Execution{ID: key.id, AgentID: key.agent, Status: "in_progress", Usage: []ModelUsage{}, SourceIDs: []string{}}
	var start, end, status *adapter.Event
	for i := range events {
		event := &events[i]
		execution.SourceIDs = append(execution.SourceIDs, event.ID)
		if event.Status != "" && (status == nil || event.Time.After(status.Time)) {
			status = event
		}
		if event.ParentExecution != "" && execution.ParentExecutionID == nil {
			value := event.ParentExecution
			execution.ParentExecutionID = &value
		}
		if event.Kind == "spawn" && event.ReferencedExecution == "" && !event.Time.IsZero() && execution.SpawnedAt == nil {
			value := event.Time
			execution.SpawnedAt = &value
		}
		if (event.Kind == "task_start" || event.Kind == "execution_start") && (start == nil || (start.Time.IsZero() && !event.Time.IsZero()) || (!event.Time.IsZero() && event.Time.Before(start.Time))) {
			start = event
		}
		if (event.Kind == "task_end" || event.Kind == "execution_end") && (end == nil || (end.Time.IsZero() && !event.Time.IsZero()) || (!event.Time.IsZero() && event.Time.After(end.Time))) {
			end = event
		}
	}
	usage := aggregateUsage(key.id, key.agent, events)
	execution.Usage = usage.usage
	if execution.Usage == nil {
		execution.Usage = []ModelUsage{}
	}
	for _, diagnostic := range usage.diagnostics {
		report.add(diagnostic)
	}
	if start != nil && !start.Time.IsZero() {
		value := start.Time
		execution.StartedAt = &value
	}
	if end != nil {
		execution.Status = firstStatus(end.Status, "completed")
		if !end.Time.IsZero() {
			value := end.Time
			execution.EndedAt = &value
		}
	} else if status != nil {
		execution.Status = status.Status
	}
	switch {
	case execution.StartedAt == nil || execution.EndedAt == nil:
		report.add(Diagnostic{Code: "missing_boundary", Message: fmt.Sprintf("execution %s lacks an explicit timestamped start or terminal event", key.id), Scope: "execution", AgentID: key.agent, ExecutionID: &key.id, Metric: "elapsed"})
		// Without a terminal event the counters on hand are a subtotal, not a final total.
		if end == nil && usage.completeness == "complete" {
			usage.completeness = "partial"
		}
	case execution.EndedAt.Before(*execution.StartedAt):
		execution.ElapsedMilliseconds = nil
		report.add(Diagnostic{Code: "invalid_boundary", Message: fmt.Sprintf("execution %s ends before it starts", key.id), Scope: "execution", AgentID: key.agent, ExecutionID: &key.id, Metric: "elapsed"})
	default:
		value := execution.EndedAt.Sub(*execution.StartedAt).Milliseconds()
		execution.ElapsedMilliseconds = &value
	}
	return execution, usage.completeness
}

func (report *ExecutionReport) add(diagnostic Diagnostic) {
	if affectsCompleteness(diagnostic.Code) && report.Completeness == "complete" {
		report.Completeness = "partial"
	}
	report.Diagnostics = append(report.Diagnostics, diagnostic)
}

func affectsCompleteness(code string) bool {
	return code != "unknown_provider"
}

func relevantDiagnostics(diagnostics []adapter.Diagnostic, native string, included map[executionKey]bool) []Diagnostic {
	var out []Diagnostic
	for _, diagnostic := range diagnostics {
		if !diagnosticRelevant(diagnostic, native, included) {
			continue
		}
		out = append(out, fromAdapter(diagnostic))
	}
	return out
}

func diagnosticRelevant(diagnostic adapter.Diagnostic, native string, included map[executionKey]bool) bool {
	switch diagnostic.Scope {
	case "source":
		return true
	case "agent":
		if diagnostic.AgentID == native {
			return true
		}
		for key := range included {
			if key.agent == diagnostic.AgentID {
				return true
			}
		}
		return false
	case "execution":
		if diagnostic.Execution == nil || included == nil {
			return false
		}
		if diagnostic.Execution.AgentID != "" {
			return included[executionKey{diagnostic.Execution.ExecutionID, diagnostic.Execution.AgentID}]
		}
		for key := range included {
			if key.id == diagnostic.Execution.ExecutionID {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func fromAdapter(diagnostic adapter.Diagnostic) Diagnostic {
	out := Diagnostic{
		Code:           diagnostic.Code,
		Message:        diagnostic.Message,
		Scope:          diagnostic.Scope,
		AgentID:        diagnostic.AgentID,
		RelatedAgentID: diagnostic.RelatedAgentID,
		Metric:         diagnostic.Metric,
		SourceID:       diagnostic.SourceID,
	}
	if diagnostic.Execution != nil && diagnostic.Execution.ExecutionID != "" {
		value := diagnostic.Execution.ExecutionID
		out.ExecutionID = &value
	}
	if out.AgentID == "" && diagnostic.Execution != nil {
		out.AgentID = diagnostic.Execution.AgentID
	}
	return out
}

func hasResolutionFailure(diagnostics []Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		switch diagnostic.Code {
		case "agent_not_found", "execution_not_found", "ambiguous_execution", "ambiguous_reference", "invalid_source", "empty_source", "source_not_found", "unsupported_source", "unsupported_product", "read_error":
			return true
		}
	}
	return false
}

func delegationParent(delegation adapter.Delegation, included map[executionKey]bool, eventExec map[string]executionKey) (executionKey, bool) {
	if delegation.Parent != nil && delegation.Parent.ExecutionID != "" {
		if delegation.Parent.AgentID != "" {
			key := executionKey{delegation.Parent.ExecutionID, delegation.Parent.AgentID}
			if included[key] {
				return key, true
			}
			return executionKey{}, false
		}
		for key := range included {
			if key.id == delegation.Parent.ExecutionID {
				return key, true
			}
		}
		return executionKey{}, false
	}
	if delegation.SourceID != "" {
		if key, ok := eventExec[delegation.SourceID]; ok && included[key] {
			return key, true
		}
	}
	return executionKey{}, false
}

func childKey(delegation adapter.Delegation, keysByID map[string]map[executionKey]bool, ambiguous map[string]bool) (executionKey, bool) {
	id := delegation.Child.ExecutionID
	agent := delegation.Child.AgentID
	if agent == "" {
		agent = delegation.ChildAgentID
	}
	if agent != "" {
		key := executionKey{id, agent}
		if len(keysByID[id]) > 1 && !keysByID[id][key] {
			ambiguous[id] = true
			return executionKey{}, false
		}
		return key, true
	}
	if len(keysByID[id]) == 1 {
		for key := range keysByID[id] {
			return key, true
		}
	}
	if len(keysByID[id]) > 1 {
		ambiguous[id] = true
		return executionKey{}, false
	}
	return executionKey{id: id}, true
}

func agentHasUnscoped(events []adapter.Event, agent string) bool {
	for _, event := range events {
		if event.Session == agent && event.Execution == "" && (event.Kind == "usage" || hasUsageCounters(event)) {
			return true
		}
	}
	return false
}

func unattributed(event adapter.Event) UnattributedUsage {
	return UnattributedUsage{
		AgentID:  event.Session,
		SourceID: event.ID,
		Provider: strPtr(event.Provider),
		Model:    strPtr(event.Model),
		Tokens:   Tokens{Input: event.Input, CacheRead: event.CacheRead, CacheWrite: event.CacheWrite, Output: event.Output, Reasoning: event.Reasoning, Total: event.Total},
	}
}

func subtreeCompleteness(usage map[executionKey]string, missing bool, subtree []ModelUsage) string {
	worst := "complete"
	if missing {
		worst = "partial"
	}
	for _, completeness := range usage {
		worst = worse(worst, completeness)
	}
	if worst == "incomplete" && len(subtree) > 0 {
		return "partial"
	}
	if worst == "complete" && missing {
		return "partial"
	}
	return worst
}

func definitionFrom(events []adapter.Event, start bool) string {
	event := boundaryEvent(events, start)
	if event == nil {
		if start {
			return "explicit execution start event"
		}
		return "explicit execution terminal event"
	}
	if start && event.StartDefinition != "" {
		return event.StartDefinition
	}
	if !start && event.EndDefinition != "" {
		return event.EndDefinition
	}
	if start {
		return "explicit execution start event"
	}
	return "explicit execution terminal event"
}

func sourceID(events []adapter.Event, start bool) *string {
	event := boundaryEvent(events, start)
	if event == nil || event.ID == "" {
		return nil
	}
	value := event.ID
	return &value
}

func boundaryEvent(events []adapter.Event, start bool) *adapter.Event {
	var chosen *adapter.Event
	for i := range events {
		event := &events[i]
		kindStart := event.Kind == "task_start" || event.Kind == "execution_start"
		kindEnd := event.Kind == "task_end" || event.Kind == "execution_end"
		if start && !kindStart || !start && !kindEnd {
			continue
		}
		if chosen == nil || (chosen.Time.IsZero() && !event.Time.IsZero()) || (!event.Time.IsZero() && ((start && event.Time.Before(chosen.Time)) || (!start && event.Time.After(chosen.Time)))) {
			chosen = event
		}
	}
	return chosen
}

func firstStatus(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

func mapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
