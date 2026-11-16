# agent-metrics

`agent-metrics` is a read-only Go CLI for measuring **one sub-agent task
execution**. It reports wall-clock time and token usage for the target itself
and for its explicitly delegated subtree. It does not orchestrate tasks or score
output. Prompt text, tool arguments, tool results, and credentials are never
copied into the report.

## Build and collect

```sh
go build ./...
go test ./...
CGO_ENABLED=0 go build ./...

# One recorded invocation of the identifier returned at spawn time
agent-metrics collect --product codex --agent /root/capture_target

# A reused agent must name the invocation
agent-metrics collect --product codex --agent /root/capture_target \
  --execution TURN_ID

# Explicit records override product configuration and the default location
agent-metrics collect --product codex --agent /root/capture_target \
  --records ./sessions
```

Products are `claude-code`, `codex`, and `opencode-v2`. The record location is
chosen in this order: `--records`, then the product setting, then the default.
An invalid or empty higher-priority location is reported and is not replaced by
a lower-priority one.

| Product | Setting | Default |
| --- | --- | --- |
| `claude-code` | `$CLAUDE_CONFIG_DIR/projects` | `~/.claude/projects` |
| `codex` | `$CODEX_HOME/sessions` | `~/.codex/sessions` |
| `opencode-v2` | `$OPENCODE_DB` | `~/.local/share/opencode/opencode.db` |

A relative `OPENCODE_DB` is resolved from `~/.local/share/opencode`, which is
the data directory used by the captured OpenCode CLI, not from the working
directory. `--records` for OpenCode must be one database file. A directory of
database snapshots is not scanned.

Exit status `2` means the command line is invalid. Exit status `1` means the
record location or the requested execution could not be resolved; the JSON
report still explains that with a diagnostic. Exit status `0` means one
execution was measured. Its `status` may still be `running`, `interrupted`, or
`succeeded`, and its `completeness` may be `partial`.

## Identifiers and execution selection

`--agent` is the identifier returned when the sub-agent was created. It is not
a shared parent session id.

* **Claude Code:** the Agent tool result `agentId`. One invocation is the `uuid`
  of the user record that starts it: the initial spawn prompt, or a later
  `origin.kind=coordinator` record. The transcript is
  `projects/<session>/subagents/agent-<agentId>.jsonl`.
* **Codex:** the returned task path, such as `/root/capture_target`. One
  invocation is the child rollout's `task_started.turn_id`. The path is resolved
  through `session_meta.agent_path` and `SubAgentActivity`; the shared
  `session_id` is not the child id. If the same path appears in more than one
  thread, the command does not pick the newest file.
* **OpenCode V2:** the returned `sessionID`. One invocation is the id of the
  user `session_message` that starts it. The execution ends at the following
  `idle` record, not when the parent's subagent tool reports `completed`.

If the agent has one invocation, it is selected. If it has more than one and
`--execution` is omitted, the report is `incomplete` with `ambiguous_execution`
and the selectable ids. Another execution is never substituted.

## Measurement

Schema `2.1` keeps the `target`, `timing`, `usage`, `executions`, and
`diagnostics` fields and adds usage completeness plus `unattributed_usage`.

* `target.agent_id` is the identifier you passed. `target.session_id` is the
  resolved native session or thread when that is a different value, and is
  `null` when the returned id is already that identity or no session was
  recorded.
* `timing` is the target execution's own explicit start-to-terminal wall clock.
  Descendant durations are not added. A missing or reversed boundary leaves
  elapsed time `null`.
* `usage.self` is the target only. `usage.subtree` adds explicitly linked
  descendants. `self_completeness` and `subtree_completeness` are separate, so
  a missing child can leave self usage and elapsed time intact while the
  subtree is `partial`.
* Counters that were not observed stay `null`. An observed zero stays `0`.
  OpenCode does not record a native total, so `total` stays `null` without
  making an otherwise complete measurement partial.
* Usage is counted once per response or message id. A repeated id with
  different counters is `duplicate_conflict` and is not silently chosen.
  Cumulative snapshots and notification totals are not added.
* A provider or model that the record does not contain stays `null`. Claude
  Code transcripts have no provider field, so the report keeps the model and
  the counters and adds `unknown_provider`. The model name is not used to
  invent a provider.
* `unattributed_usage` holds counters whose execution could not be determined.
  They are not added to self or subtree, and a missing start id is not replaced
  with a generated one.

The boundary and counter definitions for each captured product are in
[docs/native-support.md](docs/native-support.md).

## Verified scope

The native readers are checked by replaying the sanitized records under
`testdata/<product>/native`. Those records come from Claude Code **2.1.312**,
Codex **0.165.0**, and OpenCode **2.2.1**, for the launch paths described in
each fixture `capture.json`. The replay checks the returned id, the standard
location, configuration overrides, partial and reused executions, and that
record bodies are not emitted.

This repository checkout did not run those product binaries or read a live home
directory. Fixture replay is not a live compatibility test. Forks, compaction,
counter resets, model switches inside one execution, and launch paths that were
not captured remain outside the verified scope; an unrecognized record is
diagnosed rather than treated as a complete zero result.

```sh
python3 testdata/claude-code/native/verify.py
python3 testdata/codex/native/verify.py
python3 testdata/opencode-v2/native/verify.py
```
