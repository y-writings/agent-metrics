---
name: capture-opencode-metrics
description: Use when collecting or refreshing real OpenCode V2 subagent lifecycle and usage evidence for agent-metrics, especially SQLite snapshots, reused sessionIDs, nested subagents, running or interrupted sessions, and OPENCODE_DB overrides.
---

# Capture OpenCode Metrics

Read `testdata/opencode-v2/native/README.md` and `capture.json` when present.
VIS-476 owns fixture evidence; VIS-473 owns aggregation semantics and VIS-474 owns
adapter/CLI support. This guidance records observed OpenCode 2.2.1 behavior, not a
schema promise for other versions.

## Collect real evidence

Record the OpenCode version, executable hash, OS/build, model/provider/variant, and
launch mode. Use real native `subagent` calls and tiny read-only tasks; never
fabricate a call, child, usage record, boundary, or API response.

Before publishing an exact launch command or agent configuration, verify its
syntax against that installed version's `--help`, `debug config`, and `debug
agents` output. If it was not observed, state the required pre-launch condition
with placeholders instead of inventing a flag, subcommand, or config shape.

OpenCode 2.2.1 stores the observed evidence in
`<home>/.local/share/opencode/opencode.db`, chiefly `project`, `session_v2`, and
`session_message`. While OpenCode is running, take a SQLite **Online Backup** into
a private directory. Do not copy the live main DB/WAL/SHM trio, checkpoint it, or
vacuum it. Open the completed private backup with `mode=ro&immutable=1`.

| Scenario | Required native evidence |
| --- | --- |
| Spawn/nesting | Parent subagent call `state.metadata.sessionID`, child `parent_id`, and real P→T or P→T→G calls |
| Reuse | Pass the returned `sessionID` into the next subagent call; retain both child user-to-idle invocations and the first cumulative baseline |
| Running | Background call, active API says running, child has an open running tool and no idle |
| Interrupted | Active before interrupt, official interrupt response, target absent from active afterward, child idle outcome `interrupted` |
| Override | Set `OPENCODE_DB` before launching a new database-owning process and create a fresh lineage in that DB; moving an existing DB proves nothing |

A background parent tool `completed` time is spawn completion, not child completion.
For each child invocation, start at its `user` message and end at the **first and
only following `idle` before the next user**. Do not use an assistant completion,
parent tool time, final row, or session creation time. An open tail without idle is
running and has no final elapsed time.

## Counters and expectations

Sum assistant `data.tokens` once per native `responseId` inside the selected
invocation, using `session_message.id` only when `responseId` is absent. Repeated
response identities must carry identical usage and model evidence. Preserve
input, output, reasoning, cache read, and cache write. The
`session_v2.tokens_*` columns are session-lifetime cumulative counters. For reuse,
keep the pre-invocation baseline and compare `final - baseline` with the selected
message increments.

OpenCode 2.2.1 exposed **no native total counter** in these records. Keep
`total_tokens` unknown/null; never manufacture it by adding the five components.
Preserve numeric zero separately from null. Derive literal expectations from the
private native records, independently of agent-metrics code or CLI output.

## Publish by reconstruction

Build a brand-new database containing only allow-listed DDL and selected rows.
Keep linkage, ordering, types, counters, null/zero, models, and time differences;
pseudonymize identifiers consistently and shift every retained clock by one fixed
offset. Empty or remove prompts, messages, reasoning, commands, tool bodies,
credentials, account data, signed URLs, machine paths, and private source
filenames. Minimize active and
interrupt responses to the selected pseudonymous session and state.

Any reusable procedure, review note, or published command is also a disclosure
boundary. Write local locations as `<home>`, `<workspace>`, `<private-stage>`, or
another explicit placeholder; never copy an actual home, temporary, or worktree
absolute path into shareable output.

Verify hashes, exact DDL and rows, integrity, foreign keys, zero freelist, no
WAL/SHM, reference closure, boundaries, arithmetic, and privacy. Mark unobserved
patterns `uncollected`, keep real-derived records separate from derived faults,
and never present fixture validation as native adapter support.

## Common mistakes

- Scrubbing a copied live DB leaves private data in pages, freelists, or WAL.
- A parent call or assistant completion is not the child idle boundary.
- A final cumulative session value is not one reused invocation without a baseline.
- Copying a default DB under an override path does not prove `OPENCODE_DB` routing.
- Sanitized fixture rows are not enough if the accompanying instructions leak local paths.
- A plausible-looking CLI/config recipe is not evidence unless the installed version accepts it.
