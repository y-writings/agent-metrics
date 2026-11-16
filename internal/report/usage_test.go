package report

import (
	"testing"

	"github.com/y-writings/agent-metrics/internal/adapter"
)

func TestAggregateKeepsKnownZerosAndUnprovidedCounters(t *testing.T) {
	zero := int64(0)
	result := aggregateUsage("run", "agent", []adapter.Event{
		{ID: "usage", UsageID: "response-1", Session: "agent", Execution: "run", Kind: "usage", Provider: "openai", Model: "m", Input: &zero, CacheRead: &zero},
	})
	if result.completeness != "complete" || len(result.usage) != 1 || result.usage[0].Tokens.Input == nil || *result.usage[0].Tokens.Input != 0 || result.usage[0].Tokens.CacheRead == nil || *result.usage[0].Tokens.CacheRead != 0 {
		t.Fatalf("observed zero must stay zero: %+v", result)
	}
	if result.usage[0].Tokens.Output != nil || result.usage[0].Tokens.Total != nil {
		t.Fatalf("unprovided counters must stay null: %+v", result.usage[0].Tokens)
	}
}

func TestAggregateNullTotalDoesNotForcePartial(t *testing.T) {
	input := int64(3)
	result := aggregateUsage("run", "agent", []adapter.Event{
		{ID: "one", UsageID: "r1", Kind: "usage", Provider: "openai", Model: "m", Input: &input},
		{ID: "two", UsageID: "r2", Kind: "usage", Provider: "openai", Model: "m", Input: &input},
	})
	if result.completeness != "complete" || result.usage[0].Tokens.Total != nil || *result.usage[0].Tokens.Input != 6 {
		t.Fatalf("a counter absent from every record is unknown, not partial: %+v", result)
	}
}

func TestAggregateDeduplicatesMatchingUsageIdentity(t *testing.T) {
	input := int64(10)
	result := aggregateUsage("run", "agent", []adapter.Event{
		{ID: "row-1", UsageID: "same", Kind: "usage", Provider: "openai", Model: "m", Input: &input},
		{ID: "row-2", UsageID: "same", Kind: "usage", Provider: "openai", Model: "m", Input: &input},
	})
	if result.completeness != "complete" || len(result.usage) != 1 || *result.usage[0].Tokens.Input != 10 {
		t.Fatalf("matching duplicate must count once: %+v", result)
	}
}

func TestAggregateDoesNotChooseConflictingDuplicate(t *testing.T) {
	first := int64(10)
	second := int64(12)
	other := int64(4)
	result := aggregateUsage("run", "agent", []adapter.Event{
		{ID: "a", UsageID: "same", Kind: "usage", Provider: "openai", Model: "m", Input: &first},
		{ID: "b", UsageID: "same", Kind: "usage", Provider: "openai", Model: "m", Input: &second},
		{ID: "c", UsageID: "other", Kind: "usage", Provider: "openai", Model: "m", Input: &other},
	})
	if result.completeness != "partial" || len(result.usage) != 1 || result.usage[0].Tokens.Input == nil || *result.usage[0].Tokens.Input != 4 {
		t.Fatalf("conflict must not be summed or picked; other known usage remains: %+v", result)
	}
	found := false
	for _, diagnostic := range result.diagnostics {
		if diagnostic.Code == "duplicate_conflict" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing conflict diagnostic: %+v", result.diagnostics)
	}
}

func TestAggregateMixedMissingCounterStaysUnknown(t *testing.T) {
	inputA := int64(10)
	inputB := int64(5)
	outputA := int64(1)
	result := aggregateUsage("run", "agent", []adapter.Event{
		{ID: "a", UsageID: "a", Kind: "usage", Provider: "openai", Model: "m", Input: &inputA, Output: &outputA},
		{ID: "b", UsageID: "b", Kind: "usage", Provider: "openai", Model: "m", Input: &inputB},
	})
	if result.completeness != "partial" || *result.usage[0].Tokens.Input != 15 || result.usage[0].Tokens.Output != nil {
		t.Fatalf("a counter missing on one record must not be reported as the other record's value: %+v", result)
	}
}

func TestAggregateDropsRawCumulative(t *testing.T) {
	delta := int64(2)
	cumulative := int64(100)
	result := aggregateUsage("run", "agent", []adapter.Event{
		{ID: "delta", UsageID: "d", Kind: "usage", Provider: "openai", Model: "m", Input: &delta},
		{ID: "snap", UsageID: "s", Kind: "usage", Provider: "openai", Model: "m", Input: &cumulative, Cumulative: true},
	})
	if result.completeness != "partial" || len(result.usage) != 1 || *result.usage[0].Tokens.Input != 2 {
		t.Fatalf("raw cumulative must not be added: %+v", result)
	}
}

func TestAggregateRetainsUnknownModel(t *testing.T) {
	input := int64(7)
	result := aggregateUsage("run", "agent", []adapter.Event{
		{ID: "usage", UsageID: "u", Kind: "usage", Input: &input},
	})
	if result.completeness != "partial" || len(result.usage) != 1 || result.usage[0].Model != nil || result.usage[0].Provider != nil || *result.usage[0].Tokens.Input != 7 {
		t.Fatalf("unknown model must keep the counters in a null group: %+v", result)
	}
}
