---
name: capture-codex-metrics
description: Capture real Codex subagent lifecycle and usage fixtures for agent-metrics. Use for requested native-log collection, not ordinary coding tasks or fabricated example logs.
---

Read the Codex capture ledger in `testdata/codex/native/README.md` when present.
The fixture contract comes from VIS-476, with execution semantics in VIS-473
and adapter ownership in VIS-474.

Use actual Codex collaboration tools for the requested scenario. A returned
task path can be the public agent reference; retain its native mapping to the
child thread UUID. Parent and child turns need not have the same ID. Prefer
small read-only tasks against README.md. Never simulate a tool result, usage,
boundary, or child log. Only delegate when the capture scenario requests it.

For a running/interrupted capture, read a small file, then use a bounded sleep
(at most 45 seconds per command). The parent must snapshot the real records
before interrupting via the normal collaboration tool. An interrupted request
may be resumed only as an explicitly documented continuation. A completed
request followed by another request is a separate execution.

Read original logs without modifying them. Keep private snapshots outside the
repository. Record the actual build, launch mode, model/provider, save settings,
returned reference, and native source. A CODEX_HOME override must be in effect
before launching the process; relocating a copied file proves nothing.

Publish only minimized native records. Remove message/reasoning/tool bodies,
instructions, credentials, account metadata, machine paths, and encrypted
payloads. Retain required call and child references inside their original
string fields. Preserve numeric counters, types, event order, nulls, ID shape
and referential integrity. Shift all clocks consistently if shifting time.

Derive expected values from native per-response usage and explicit boundaries,
independently of agent-metrics code. Compare request sums with cumulative
differences and keep nonzero baselines for reused threads. Do not add cache or
reasoning subsets to total. Record unknown values and their reasons. Distinguish
real-derived captures from derived fault injections and synthetic data.

Mark unobserved patterns as uncollected; call a pattern inapplicable only with
product evidence. Keep CLI support claims separate from fixture validation.
