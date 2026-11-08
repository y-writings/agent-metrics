---
name: capture-claude-code-metrics
description: Capture real Claude Code subagent lifecycle and usage fixtures for agent-metrics (VIS-476). Use only for requested native-log collection, not ordinary coding tasks or fabricated example logs.
---

Read the Claude Code capture ledger in `testdata/claude-code/native/README.md`
when present. The fixture contract comes from VIS-476, execution semantics from
VIS-473 and adapter ownership from VIS-474. Codex/OpenCode captures are done by
their own products; never imitate them.

## Where the records are

Run `claude --version` and record it. Without `CLAUDE_CONFIG_DIR`, transcripts
live under `~/.claude/projects/<cwd with / and . replaced by ->/`:

- `<session-id>.jsonl` is the main thread (`isSidechain=false`).
- `<session-id>/subagents/agent-<agentId>.jsonl` is each subagent
  (`isSidechain=true`, `agentId`). Grandchildren are stored flat in the same
  directory; their `agent-<id>.meta.json` has `parentAgentId` and
  `spawnDepth`. `meta.json` is rewritten in place (`finished`), so snapshot it.
- `$CLAUDE_CODE_SESSION_ID` is this session. Do not read
  `/private/tmp/.../tasks/*.output`: it is a symlink to the same transcript.

## Scenario operations

Use the real Agent/SendMessage/TaskStop tools. Use `general-purpose` (it
returns an `agentId` and can be resumed); do not use Explore/Plan as the base
case. Prefer tiny read-only tasks (first line of a repository file).

- R1: Agent spawn. The parent `tool_result.toolUseResult.agentId` is the
  returned reference; `status=async_launched`, `resolvedModel`.
- R2: spawn a sibling concurrently; ask the target to spawn a grandchild
  (optionally `model: "haiku"` for a mixed-model subtree).
- R3: `SendMessage` to the same agentId after it finished. The child gets a
  new `user` record with `origin.kind=coordinator`; that is the next execution.
- R5: background subagents keep running while you work. The harness blocks
  foreground `sleep`; do not circumvent it. Make the task naturally long
  (sequential one-file reads) and run `snapshot.sh PROJECT SESSION DEST 120`
  in the background for per-second read-only snapshots. Interrupt with
  `TaskStop` only.
- L2: start a _new_ `claude -p` process with `CLAUDE_CONFIG_DIR` set before
  launch. Copying files elsewhere proves nothing.

## Snapshot and sanitize

Copy only complete JSONL lines into a private directory outside the
repository with `snapshot.sh PROJECT SESSION DEST`; never modify `~/.claude`.
Minimize the private copies to the observed lifecycle/usage fields. Remove
prompt/message/thinking/tool bodies, tool inputs, system prompts, API request
blobs, git/cwd/classifier context and machine paths. Keep required references in
their native fields, pseudonymize IDs consistently, and shift every clock by
the same offset. Compare against the originals and inspect the output before
saving. Capture-specific transformation scripts stay outside the maintained
fixture bundle; keep the sanitized records, provenance and independent values.

## Expected values

Derive them by hand/independent script from the private snapshot, never from
agent-metrics output. Assistant rows are split per content block and repeat
the same `message.id`/`requestId` and `usage`: count each message once.
`total = input + cache_creation + cache_read + output`; thinking tokens are a
subset of output. The task-notification `subagent_tokens` is the last request's
context size, not consumption. Record unknowns with reasons, and mark patterns
`uncollected` unless product evidence shows they are inapplicable.

An invocation runs from its first user record (or an `origin.kind=coordinator`
record) to just before the next coordinator record. Its terminal is the last
`end_turn` / `toolEndsTurn` / `[Request interrupted by user]` record, and only
when the caller received a stop notification afterwards. A SubagentHandback is
not the terminal: the child keeps consuming turns while its own background
children report back, and the notification waits for them. An `api-request`
without an assistant response (interrupted in flight) means unknown usage.
stream-json usage is not final; persisted transcripts are the usage source.
