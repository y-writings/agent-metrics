package report

import (
	"fmt"
	"sort"

	"github.com/y-writings/agent-metrics/internal/adapter"
)

type modelKey struct{ provider, model string }

type usageResult struct {
	usage        []ModelUsage
	completeness string
	diagnostics  []Diagnostic
}

func aggregateUsage(executionID, agentID string, events []adapter.Event) usageResult {
	result := usageResult{completeness: "complete"}
	order := []string{}
	groups := map[string][]adapter.Event{}
	sawEmpty := false
	for _, event := range events {
		hasCounters := hasUsageCounters(event)
		if event.Kind != "usage" && !hasCounters {
			continue
		}
		if event.Cumulative {
			result.completeness = worse(result.completeness, "partial")
			result.diagnostics = append(result.diagnostics, executionDiagnostic("raw_cumulative", fmt.Sprintf("usage %s is a raw cumulative snapshot and was not added", usageIdentity(event)), agentID, executionID, "usage", event.ID))
			continue
		}
		if !hasCounters {
			sawEmpty = true
			result.completeness = worse(result.completeness, "partial")
			result.diagnostics = append(result.diagnostics, executionDiagnostic("unknown_usage", fmt.Sprintf("usage event %s has no observed token counters", event.ID), agentID, executionID, "usage", event.ID))
			continue
		}
		id := usageIdentity(event)
		if _, ok := groups[id]; !ok {
			order = append(order, id)
		}
		groups[id] = append(groups[id], event)
	}
	var kept []adapter.Event
	for _, id := range order {
		events := groups[id]
		if len(events) > 1 {
			matched := true
			for _, event := range events[1:] {
				if !sameUsage(events[0], event) {
					matched = false
					break
				}
			}
			if !matched {
				result.completeness = worse(result.completeness, "partial")
				result.diagnostics = append(result.diagnostics, executionDiagnostic("duplicate_conflict", fmt.Sprintf("usage %s was repeated with different counters and was not counted", id), agentID, executionID, "usage", events[0].ID))
				continue
			}
		}
		kept = append(kept, events[0])
		if events[0].Model == "" {
			result.completeness = worse(result.completeness, "partial")
			result.diagnostics = append(result.diagnostics, executionDiagnostic("unknown_model", fmt.Sprintf("usage %s has no model", usageIdentity(events[0])), agentID, executionID, "model", events[0].ID))
		}
	}
	if len(kept) == 0 {
		if result.completeness == "complete" || sawEmpty {
			result.completeness = "incomplete"
		}
		if !sawEmpty && result.completeness == "incomplete" {
			result.diagnostics = append(result.diagnostics, executionDiagnostic("unknown_usage", fmt.Sprintf("execution %s has no observed usage counters", executionID), agentID, executionID, "usage", ""))
		}
		return result
	}
	byModel := map[modelKey][]adapter.Event{}
	var modelOrder []modelKey
	for _, event := range kept {
		key := modelKey{event.Provider, event.Model}
		if _, ok := byModel[key]; !ok {
			modelOrder = append(modelOrder, key)
		}
		byModel[key] = append(byModel[key], event)
	}
	for _, key := range modelOrder {
		tokens, partials := foldTokens(byModel[key])
		for _, metric := range partials {
			result.completeness = worse(result.completeness, "partial")
			result.diagnostics = append(result.diagnostics, executionDiagnostic("unknown_counter", fmt.Sprintf("execution %s is missing %s on some usage records", executionID, metric), agentID, executionID, metric, ""))
		}
		result.usage = append(result.usage, ModelUsage{Provider: strPtr(key.provider), Model: strPtr(key.model), Tokens: tokens})
	}
	sort.Slice(result.usage, func(i, j int) bool {
		return modelLess(result.usage[i], result.usage[j])
	})
	return result
}

func modelLess(a, b ModelUsage) bool {
	if deref(a.Provider) == deref(b.Provider) {
		return deref(a.Model) < deref(b.Model)
	}
	return deref(a.Provider) < deref(b.Provider)
}

func hasUsageCounters(event adapter.Event) bool {
	return event.Input != nil || event.CacheRead != nil || event.CacheWrite != nil || event.Output != nil || event.Reasoning != nil || event.Total != nil
}

func usageIdentity(event adapter.Event) string {
	if event.UsageID != "" {
		return event.UsageID
	}
	return event.ID
}

func sameUsage(a, b adapter.Event) bool {
	return a.Provider == b.Provider && a.Model == b.Model && sameInt(a.Input, b.Input) && sameInt(a.CacheRead, b.CacheRead) && sameInt(a.CacheWrite, b.CacheWrite) && sameInt(a.Output, b.Output) && sameInt(a.Reasoning, b.Reasoning) && sameInt(a.Total, b.Total)
}

func sameInt(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func foldTokens(events []adapter.Event) (Tokens, []string) {
	var tokens Tokens
	var partials []string
	tokens.Input, partials = foldCounter(events, partials, "input", func(event adapter.Event) *int64 { return event.Input })
	tokens.CacheRead, partials = foldCounter(events, partials, "cache_read", func(event adapter.Event) *int64 { return event.CacheRead })
	tokens.CacheWrite, partials = foldCounter(events, partials, "cache_write", func(event adapter.Event) *int64 { return event.CacheWrite })
	tokens.Output, partials = foldCounter(events, partials, "output", func(event adapter.Event) *int64 { return event.Output })
	tokens.Reasoning, partials = foldCounter(events, partials, "reasoning", func(event adapter.Event) *int64 { return event.Reasoning })
	tokens.Total, partials = foldCounter(events, partials, "total", func(event adapter.Event) *int64 { return event.Total })
	return tokens, partials
}

func foldCounter(events []adapter.Event, partials []string, metric string, get func(adapter.Event) *int64) (*int64, []string) {
	var sum int64
	seen := 0
	for _, event := range events {
		value := get(event)
		if value == nil {
			continue
		}
		seen++
		sum += *value
	}
	if seen == 0 {
		return nil, partials
	}
	if seen != len(events) {
		return nil, append(partials, metric)
	}
	return &sum, partials
}

func worse(current, next string) string {
	rank := map[string]int{"complete": 0, "partial": 1, "incomplete": 2}
	if rank[next] > rank[current] {
		return next
	}
	return current
}

func strPtr(value string) *string {
	if value == "" {
		return nil
	}
	copied := value
	return &copied
}

func executionDiagnostic(code, message, agentID, executionID, metric, sourceID string) Diagnostic {
	diagnostic := Diagnostic{Code: code, Message: message, Scope: "execution", AgentID: agentID, Metric: metric, SourceID: sourceID}
	if executionID != "" {
		diagnostic.ExecutionID = &executionID
	}
	return diagnostic
}

func foldUsageTokens(rows []Tokens) (Tokens, []string) {
	var tokens Tokens
	var partials []string
	tokens.Input, partials = foldUsageCounter(rows, partials, "input", func(row Tokens) *int64 { return row.Input })
	tokens.CacheRead, partials = foldUsageCounter(rows, partials, "cache_read", func(row Tokens) *int64 { return row.CacheRead })
	tokens.CacheWrite, partials = foldUsageCounter(rows, partials, "cache_write", func(row Tokens) *int64 { return row.CacheWrite })
	tokens.Output, partials = foldUsageCounter(rows, partials, "output", func(row Tokens) *int64 { return row.Output })
	tokens.Reasoning, partials = foldUsageCounter(rows, partials, "reasoning", func(row Tokens) *int64 { return row.Reasoning })
	tokens.Total, partials = foldUsageCounter(rows, partials, "total", func(row Tokens) *int64 { return row.Total })
	return tokens, partials
}

func foldUsageCounter(rows []Tokens, partials []string, metric string, get func(Tokens) *int64) (*int64, []string) {
	var sum int64
	seen := 0
	for _, row := range rows {
		value := get(row)
		if value == nil {
			continue
		}
		seen++
		sum += *value
	}
	if seen == 0 {
		return nil, partials
	}
	if seen != len(rows) {
		return nil, append(partials, metric)
	}
	return &sum, partials
}
