# agent-metrics

`agent-metrics` is a read-only Go CLI for measuring **one sub-agent task
execution**. It reports information that cannot be judged from the artifact
alone: wall-clock time and token usage for the target itself and for its whole
explicitly delegated subtree. It neither orchestrates tasks nor scores output.
Prompt/message contents, tool arguments/results, and credentials are never
copied to the report.

## Build and collect

```sh
go build ./...
go test ./...

# A new sub-agent with exactly one recorded invocation
agent-metrics collect --product codex --agent AGENT_ID

# A reused agent must name the invocation
agent-metrics collect --product codex --agent AGENT_ID --execution EXECUTION_ID

# Fixture, export, or nonstandard installation
agent-metrics collect --product codex --agent AGENT_ID \
  --execution EXECUTION_ID --records ./records
```

Products are `claude-code`, `codex`, and `opencode-v2`. Without `--records`,
the search roots are respectively `~/.claude/projects`, `~/.codex/sessions`,
and `~/.local/share/opencode/storage`. Record files are only read. A directory
is recursively scanned for `.json` and `.jsonl` files.

The identifier returned by sub-agent creation is passed to `--agent`. If its
records contain one execution, it is selected. If the same agent was invoked
more than once, collection returns `incomplete` with an
`ambiguous_execution` diagnostic containing the selectable IDs; it never picks
the latest invocation or measures the whole session implicitly.

## Record connection and supported scope

Adapters normalize lifecycle and usage metadata only. Accepted identifier
aliases include `agent_id`/`session_id`/`thread_id`, `execution_id`/
`invocation_id`/`run_id`, `parent_execution_id`, and `child_execution_id`.
`task_id` and `turn_id` remain source identifiers; they are not required to be
equal between parent and child. A missing `execution_id` falls back to the
record's task/turn ID for export fixtures. Fallback IDs are scoped to the
agent, so another agent with the same task/turn ID cannot contribute usage.

An execution subtree is followed only through explicit
`parent_execution_id` or `child_execution_id` references. Timestamps, working
directories, and similar coincidental attributes are never used to infer a
relationship. Consequently parent preparation, sibling trials, earlier/later
invocations, and orchestrator usage are excluded, while explicitly delegated
descendants are recursive. A referenced execution without records produces a
`missing_descendant` diagnostic. If a delegation reference names an ID shared
by multiple agents, the subtree is partial with an `ambiguous_execution_link`
diagnostic; the collector does not guess which child was intended.

> **Current validation scope:** the adapters support the documented metadata
> aliases in JSON/JSONL records. The default roots make native record discovery
> possible, but the current fixtures are synthetic rather than captures from
> released Claude Code, Codex, or OpenCode V2 builds. Product-version-specific
> native layouts that do not expose these fields are reported as unsupported;
> they are not treated as complete results.

## Schema 2.0

The command emits schema `2.0`:

* `target` identifies the product, requested agent, resolved execution, and
  source session/thread when recorded (`null` when absent).
* `timing` is the target execution's explicit start-to-terminal wall clock,
  with definitions and source record IDs. Descendant durations are not added.
* `usage.self` is target-only usage; `usage.subtree` is target plus all resolved
  descendants, grouped by provider and model.
* `executions[]` contains each invocation's execution and agent IDs, parent
  execution, spawn/start/end/elapsed values, state, own usage, and source IDs.
* `status` is execution outcome. `completeness` (`complete`, `partial`, or
  `incomplete`) independently describes whether the metric is conclusive, and
  `diagnostics` gives machine-readable reasons.

Start boundaries are `execution_start` or `task_start`; terminal boundaries are
`execution_end` or `task_end`. A final log timestamp and spawn time are never
substituted. Missing timestamps or reversed boundaries leave elapsed time
`null`. Target elapsed time remains available when descendant data is partial.
A failure is an execution state and does not by itself mean that observed data
is incomplete.

Usage preserves input, cache read/write, output, reasoning, and total counters.
These are separate observations: cache/reasoning values are not added again to
total. Missing is `null`, while observed zero is `0`. Events are deduplicated by
source ID within an execution, and usage is combined only after execution
scope is resolved. Cumulative counters are scoped per execution/provider/model;
exports must provide execution-normalized deltas (or a baseline and derived
increment) rather than a session-lifetime snapshot. If a reliable baseline,
counter meaning, reset, or model attribution cannot be established, affected
values must stay unknown with a diagnostic rather than being guessed.

Completeness can only guarantee what records expose. Explicitly referenced but
missing descendants, missing terminal boundaries, malformed files, usage with
no counters, and unknown model attribution make a report partial. If a product
record names an included agent but has usage counters without an execution or
task/turn ID, its usage remains unattributed and the report is partial. If a
product emits no spawn reference at all, local records cannot prove that an unseen
child did not exist; this is a source-format limitation rather than evidence of
complete subtree collection.

## Safe fixture example

Fixtures should contain metadata and numeric counters only—never real prompt,
message, tool, or authentication content:

```jsonl
{"id":"start","agent_id":"AGENT_ID","execution_id":"RUN_ID","kind":"execution_start","timestamp":"2026-01-01T00:00:00Z"}
{"id":"usage","agent_id":"AGENT_ID","execution_id":"RUN_ID","kind":"usage","provider":"openai","model":"example-model","usage":{"input":10,"output":2,"total":12}}
{"id":"end","agent_id":"AGENT_ID","execution_id":"RUN_ID","kind":"execution_end","timestamp":"2026-01-01T00:00:01Z","status":"completed"}
```

Automated tests separate common selection/aggregation contracts from adapter
normalization. Synthetic fixtures verify reused-agent ambiguity, independent
parent/child turn IDs, exclusion of historical executions, recursive scope,
deduplication, missing data, and wall-clock boundaries. They are not presented
as evidence of end-to-end compatibility with a particular product release.
