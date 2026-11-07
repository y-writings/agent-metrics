# agent-metrics

`agent-metrics` is a read-only Go CLI that creates task-scoped metrics from
Claude Code, Codex, and OpenCode V2 native JSON/JSONL records. It never emits
prompt/message/tool bodies.

## Build and run

```sh
go build ./...
go test ./...
go run ./cmd/agent-metrics -product codex -records ~/.codex/sessions \
  -parent THREAD_ID -task TURN_ID
```

Products are `claude-code`, `codex`, and `opencode-v2`. `-records` accepts a
file or recursively scans a directory for `.json` and `.jsonl` files. Supply
Claude's parent session ID and task ID, Codex's thread and turn IDs, or
OpenCode's parent session and task IDs. Explicit paths are recommended because
installations and product versions use different storage locations.

## Record mapping and scope

The reader accepts native aliases (`session_id`/`sessionID`/`thread_id`,
`parent_id`/`parentID`/`parent_thread_id`, and `task_id`/`taskID`/`turn_id`).
Boundaries are `task_start`/`task_end` or `turn_start`/`turn_end`. Usage may be
under `usage`, `tokens`, or a message's usage object. Descendants are followed
recursively using the persisted parent ID; only parent records with the exact
task/turn ID are included. Child records must retain the task ID and parent
link. A reused child therefore cannot leak messages from another invocation.

Token fields map to input, cache read, cache write, output, reasoning, and
total. These are independent observations: cache and reasoning fields are
**not** added to total. Missing fields remain `null`. Duplicate event IDs are
counted once. For records marked `cumulative: true`, only the last snapshot per
agent/model is counted, rather than adding every snapshot.

## JSON schema and incomplete data

Output schema `1.0` contains the selected task, parent wall-clock timing (UTC
RFC 3339 timestamps and milliseconds), aggregate usage by provider/model, and
each agent's parent, spawn time, status, usage, and source event IDs. Parallel
child durations are never summed. Completion time requires an explicit parent
terminal event; the final log timestamp is not substituted.

`status` describes execution, while `completeness` independently reports
`complete`, `partial`, or `incomplete`. Diagnostics explain missing boundaries,
unknown models, corrupt records, and unmatched tasks. Known zero is `0`; absent
data is `null`. A missing/non-persisted child cannot always be detected from
local logs, so completeness is guaranteed only when every spawn and descendant
record has a stable parent/task link and explicit terminal record.

## Validation scope

Automated tests use synthetic, anonymized records reproducing the documented
field shapes. They cover descendants, parallel-safe wall time, model changes,
duplicate usage, prior-turn exclusion, cumulative snapshots, reused-child
scoping, and missing terminal events. No claim is made that these fixtures are
captured real logs or that every future product format is supported; unknown
formats fail or produce an explicit diagnostic instead of guessed metrics.
