# agent-metrics

`agent-metrics` is a read-only Go CLI that creates task-scoped metrics from
JSON/JSONL exports using the envelope below and aliases associated with Claude
Code, Codex, and OpenCode V2. It never emits prompt/message/tool bodies. Direct
ingestion of each product's native storage layout is not yet validated.

## Build and run

```sh
go build ./...
go test ./...
go run ./cmd/agent-metrics -product codex -records records.jsonl \
  -parent THREAD_ID -task TURN_ID
```

Products are `claude-code`, `codex`, and `opencode-v2`. `-records` accepts a
file or recursively scans a directory for `.json` and `.jsonl` files. Supply
Claude's parent session ID and task ID, Codex's thread and turn IDs, or
OpenCode's parent session and task IDs from the export. The product option labels
the report; all three currently use the same envelope reader.

## Record mapping and scope

The reader accepts native aliases (`session_id`/`sessionID`/`thread_id`,
`parent_id`/`parentID`/`parent_thread_id`, and `task_id`/`taskID`/`turn_id`).
Boundaries are `task_start`/`task_end` or `turn_start`/`turn_end`. Usage may be
under `usage`, `tokens`, or a message's usage object, regardless of record type.
Usage-bearing boundary records retain their lifecycle meaning. Descendants are
followed recursively using parent links from the exact selected task/turn only.
Child records must retain that task ID and a stable parent link. Historical links
cannot pull a reused child into an unrelated task tree.

Example `records.jsonl` (synthetic normalized export):

```jsonl
{"id":"start","session_id":"THREAD_ID","task_id":"TURN_ID","kind":"turn_start","timestamp":"2026-01-01T00:00:00Z"}
{"id":"usage","session_id":"THREAD_ID","task_id":"TURN_ID","kind":"usage","provider":"openai","model":"example-model","usage":{"input":10,"output":2}}
{"id":"end","session_id":"THREAD_ID","task_id":"TURN_ID","kind":"turn_end","timestamp":"2026-01-01T00:00:01Z"}
```

Token fields map to input, cache read, cache write, output, reasoning, and
total. These are independent observations: cache and reasoning fields are
**not** added to total. Missing fields remain `null`. Duplicate event IDs are
counted once within each agent. For records marked `cumulative: true`, the latest
snapshot by timestamp per agent/provider/model replaces that agent's increments
for the same provider/model. Dated snapshots take precedence over undated ones;
equal timestamps (including when both are absent) retain input order as a
tie-breaker. Agent-level results are then summed; one agent's snapshot never
replaces another's usage.

Records without an event ID use the scan-root-relative file path and record
number as their source ID, so same-named files in different directories remain
distinct. For a single-file input, the source path is its filename.

## JSON schema and incomplete data

Output schema `1.0` contains the selected task, parent wall-clock timing (RFC 3339
timestamps with explicit offsets and milliseconds), aggregate usage by
provider/model, and each agent's parent, spawn time, status, usage, and source
event IDs. Parallel
child durations are never summed. Completion time requires an explicit parent
terminal event; the final log timestamp is not substituted.
Boundary timestamps must be valid RFC 3339 values in chronological order.
Missing or reversed timestamps leave elapsed time `null` and make the report
partial; an observed terminal event still determines execution status.

`status` describes execution, while `completeness` independently reports
`complete`, `partial`, or `incomplete`. Diagnostics explain missing boundaries,
unknown models or usage, corrupt records, and unmatched tasks. Known zero is `0`;
absent data is `null`. A missing/non-persisted child cannot always be detected from
local logs, so completeness is guaranteed only when every spawn and descendant
record has a stable parent/task link and explicit terminal record. An observed
descendant without a terminal event makes the report partial with a
`missing_terminal` diagnostic, even when the parent has completed.
If JSONL decoding fails, already decoded records remain available and the warning
makes the report partial. The reader does not skip ahead past malformed records.
Records without a session identifier generate a warning. Explicit usage events
without any observed counters remain unknown and produce `unknown_usage` rather
than a complete zero-usage report.

## Validation scope

Automated tests use synthetic, anonymized records reproducing the documented
field shapes. They cover descendants, parallel-safe wall time, model changes,
duplicate usage, prior-turn exclusion, cumulative snapshots, reused-child
scoping, and missing terminal events. No claim is made that these fixtures are
captured real logs or that arbitrary product-native formats are supported.
Records need explicit session/task identifiers and the documented field shapes;
unmatched tasks produce a diagnostic rather than inferred relationships.
