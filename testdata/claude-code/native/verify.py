#!/usr/bin/env python3
"""Verify frozen fixture evidence independently of agent-metrics code."""

import hashlib
import json
import re
import tempfile
from datetime import datetime
from pathlib import Path

from prepare import prepare

ROOT = Path(__file__).resolve().parent
RECORDS = ROOT / "records"
FIELDS = ["input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens"]
INTERRUPT = "[Request interrupted by user]"
SECRET = re.compile(r"/Users/|/home/|/private/|/var/folders/|/tmp/|https?://|Bearer |sk-ant-|sk-[A-Za-z0-9]{20}|[\w.+-]+@[\w-]+\.[\w.-]+|-Users-|-private-tmp-")


def read(path):
    return [json.loads(line) for line in path.read_text().splitlines()]


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def text_of(row):
    content = row.get("message", {}).get("content")
    return content if isinstance(content, str) else "".join(b.get("text", "") for b in content or [])


def privacy(value, key=""):
    forbidden = {"cwd", "gitBranch", "serverClassifierContext", "serverClassifierRequest", "permissionDecision", "wireToolInputs", "rendered", "systemPrompt", "shape", "tail", "cacheControl", "requestDigest", "outputFile", "output_file", "filePath", "lastPrompt", "aiTitle", "caller", "signature_hash"}
    if isinstance(value, dict):
        check(not forbidden.intersection(value), f"Private/body field retained: {forbidden.intersection(value)}")
        for k, child in value.items():
            if k in {"text", "thinking", "signature", "prompt", "message", "body", "handbackReportText", "summary", "result"} and isinstance(child, str):
                allowed = child == "" or child == INTERRUPT or child.startswith("<task-notification>\n<task-id>") or child.startswith("<agent-message from=")
                check(allowed, f"Body must be empty or a reduced reference string: {k}")
                if child.startswith("<task-notification>"):
                    check(set(re.findall(r"<([a-z-_]+)>", child)) <= {"task-notification", "task-id", "tool-use-id", "status", "usage", "subagent_tokens", "tool_uses", "duration_ms"}, "Unexpected notification tag")
            if k == "content" and isinstance(child, str):
                check(child in ("", INTERRUPT) or child.startswith(("<task-notification>\n<task-id>", "<agent-message from=")), "Body content retained")
            if k == "input" and isinstance(child, dict):
                check(set(child) <= {"description", "subagent_type", "model", "run_in_background", "prompt", "to", "message", "task_id"}, "Unexpected tool input field")
            privacy(child, k)
    elif isinstance(value, list):
        for child in value:
            privacy(child, key)
    elif isinstance(value, str):
        check(not SECRET.search(value), f"Private path, credential or external address retained: {value[:60]}")


def at(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def notice_fields(text):
    get = lambda tag: (re.search(f"<{tag}>(.*?)</{tag}>", text, re.S) or [None, None])[1]
    return get("status"), get("subagent_tokens"), get("duration_ms")


def verify_execution(name, e):
    rows = read(RECORDS / e["file"])
    lo, hi = e["segment_lines"]
    start = rows[lo - 1]
    check(start["type"] == "user" and start["agentId"] == e["agent_id"], f"{name}: start record")
    kind = (start.get("origin") or {}).get("kind")
    check((kind == "coordinator") == (e["start_kind"] == "coordinator_message"), f"{name}: start kind")
    check(start["timestamp"] == e["started_at"], f"{name}: start time")
    starts = [i for i, r in enumerate(rows, 1) if r["type"] == "user" and (r.get("origin") or {}).get("kind") == "coordinator"]
    check(all(not lo < s <= hi for s in starts), f"{name}: segment crosses another invocation")
    check(hi == len(rows) or hi + 1 in starts, f"{name}: segment end is not the next invocation boundary")
    meta = json.loads((RECORDS / e["file"]).with_name(Path(e["file"]).name.replace(".jsonl", ".meta.json")).read_text())
    check(meta["spawnDepth"] in (1, 2), f"{name}: meta depth")
    messages = {}
    for i, r in enumerate(rows, 1):
        if lo <= i <= hi and r["type"] == "assistant":
            m = r["message"]
            if m["id"] in messages:
                check(messages[m["id"]][1] == m["usage"], f"{name}: split rows disagree")
            else:
                messages[m["id"]] = (i, m["usage"], m["model"], r.get("requestRef"))
    check(list(messages) == e["message_ids"], f"{name}: message identities")
    check([v[0] for v in messages.values()] == e["usage_lines"], f"{name}: usage evidence lines")
    by_model = {}
    for _, u, model, _ in messages.values():
        b = by_model.setdefault(model, dict.fromkeys(FIELDS + ["thinking_tokens", "total_tokens", "cache_creation_1h", "cache_creation_5m", "messages"], 0))
        for f in FIELDS:
            b[f] += u[f]
        b["thinking_tokens"] += u["output_tokens_details"]["thinking_tokens"]
        b["cache_creation_1h"] += u["cache_creation"]["ephemeral_1h_input_tokens"]
        b["cache_creation_5m"] += u["cache_creation"]["ephemeral_5m_input_tokens"]
        b["total_tokens"] += sum(u[f] for f in FIELDS)
        b["messages"] += 1
        check(u["output_tokens_details"]["thinking_tokens"] <= u["output_tokens"], f"{name}: thinking is an output subset")
        check(sum(u["cache_creation"].values()) == u["cache_creation_input_tokens"], f"{name}: cache split is a subset")
    check(by_model == e["tokens_by_model"], f"{name}: independent usage sum")
    answered = {v[3] for v in messages.values()} | {r.get("requestRef") for r in rows if r["type"] == "assistant"}
    unanswered = [i for i, r in enumerate(rows, 1) if lo <= i <= hi and r["type"] == "api-request" and r["id"] not in answered]
    check(unanswered == e["unanswered_request_lines"], f"{name}: in-flight request evidence")
    note = e["notification"]
    if e["end_line"] is None:
        check(e["status"] == "running" and e["elapsed_ms"] is None and not e["usage_final"] and note is None, f"{name}: running is not complete")
        return
    end = rows[e["end_line"] - 1]
    terminal = (end["type"] == "assistant" and end["message"]["stop_reason"] == "end_turn") or end.get("toolEndsTurn") or text_of(end) == INTERRUPT
    check(terminal and end["timestamp"] == e["ended_at"], f"{name}: terminal record")
    check(round((at(end["timestamp"]) - at(start["timestamp"])).total_seconds() * 1000) == e["elapsed_ms"], f"{name}: wall-clock interval")
    for line in e["intermediate_turn_end_lines"]:
        check(lo <= line < e["end_line"], f"{name}: intermediate turn end outside invocation")
    stop = read(RECORDS / note["file"])[note["line"] - 1]
    check(stop["timestamp"] == note["timestamp"] and note["timestamp"] >= e["ended_at"], f"{name}: stop notification after terminal")
    if note["kind"] == "foreground_tool_result":
        result = stop["toolUseResult"]
        check(result["agentId"] == e["agent_id"] and result["status"] == "completed" and result["totalDurationMs"] == note["native_duration_ms"] and result["totalTokens"] == note["subagent_tokens"], f"{name}: foreground result")
    else:
        text = stop.get("content") if note["kind"] == "queue_operation_enqueue" else text_of(stop)
        check(f"<task-id>{e['agent_id']}</task-id>" in text, f"{name}: notification target")
        status, tokens, duration = notice_fields(text)
        check(status == note["status"] and (tokens and int(tokens)) == note["subagent_tokens"] and (duration and int(duration)) == note["native_duration_ms"], f"{name}: notification fields")
    check({"completed": "completed", "killed": "interrupted"}[note["status"]] == e["status"], f"{name}: state")


def verify():
    inventory = json.loads((RECORDS / "inventory.json").read_text())
    check({i["file"] for i in inventory} == {str(p.relative_to(RECORDS)) for p in RECORDS.rglob("*") if p.is_file() and p.name != "inventory.json"}, "Inventory/file set mismatch")
    for item in inventory:
        path = RECORDS / item["file"]
        check(hashlib.sha256(path.read_bytes()).hexdigest() == item["sha256"], f"Fixture changed: {item['file']}")
        check(not SECRET.search(item["file"]), "Machine path in fixture path")
        if path.name.endswith(".meta.json"):
            privacy(json.loads(path.read_text()))
            continue
        rows = read(path)
        check(len(rows) == len(item["source_lines"]), "Source-line mapping lost")
        for row in rows:
            privacy(row)

    oracle = json.loads((ROOT / "expected.json").read_text())
    executions = oracle["executions"]
    for name, expected in executions.items():
        verify_execution(name, expected)

    # Returned references: parent Agent call -> tool_result agentId -> child file/meta.
    for child, link in oracle["links"]["spawn"].items():
        name = {"T": "T.A", "S": "S.A", "O.child": "O.child"}.get(child, child)
        e = executions[name]
        parent_file = {"T": "default:P", "S": "default:P", "O.child": "override:O"}.get(child)
        parent_path = RECORDS / oracle["files"][parent_file]["file"] if parent_file else RECORDS / executions[{"G.B": "T.B", "G.C": "T.C", "GS": "S.B"}[child]]["file"]
        rows = read(parent_path)
        call = next(b for b in rows[link["call_line"] - 1]["message"]["content"] if b.get("type") == "tool_use" and b["id"] == link["tool_use_id"])
        result = rows[link["result_line"] - 1]
        check(call["name"] == "Agent" and result["message"]["content"][0]["tool_use_id"] == call["id"], f"{child}: spawn call/result")
        check(result["toolUseResult"]["agentId"] == link["returned_agent_id"] == e["agent_id"], f"{child}: returned agentId")
        meta = json.loads((RECORDS / e["file"]).with_name(Path(e["file"]).name.replace(".jsonl", ".meta.json")).read_text())
        check(meta["toolUseId"] == call["id"], f"{child}: meta toolUseId")
        if child in {"G.B", "G.C", "GS"}:
            parent_agent = oracle["agents"]["T" if child != "GS" else "S"]
            check(meta["parentAgentId"] == parent_agent and meta["spawnDepth"] == 2, f"{child}: grandchild lineage")
            check(all(r["agentId"] == parent_agent for r in rows if "agentId" in r), f"{child}: spawned from parent transcript")
            lo, hi = executions[{"G.B": "T.B", "G.C": "T.C", "GS": "S.B"}[child]]["segment_lines"]
            check(lo <= link["call_line"] <= hi, f"{child}: spawned within the delegating invocation")
        else:
            check("parentAgentId" not in meta and meta["spawnDepth"] == 1, f"{child}: depth-1 child")
    main = read(RECORDS / oracle["files"]["default:P"]["file"])
    resumes = oracle["links"]["resume"]
    for link, name in zip(resumes, ["T.B", "T.C", "S.B", "S.C"]):
        call = next(b for b in main[link["call_line"] - 1]["message"]["content"] if b.get("type") == "tool_use" and b["id"] == link["tool_use_id"])
        check(call["name"] == "SendMessage" and call["input"]["to"] == executions[name]["agent_id"] == main[link["result_line"] - 1]["toolUseResult"]["resumedAgentId"], f"{name}: resume reference")
        check(main[link["call_line"] - 1]["timestamp"] <= executions[name]["started_at"], f"{name}: resume precedes invocation")
    stop = oracle["links"]["stop"][0]
    check(next(b for b in main[stop["call_line"] - 1]["message"]["content"] if b.get("id") == stop["tool_use_id"])["input"]["task_id"] == executions["S.C"]["agent_id"], "TaskStop target")

    for case in oracle["cases"]:
        for field in ("self", "subtree", "known_self", "known_subtree"):
            if field in case:
                names = case.get("included", [case["target"]]) if field in ("subtree",) else [case["target"]]
                if field == "known_subtree":
                    names = ["S.B@running-grandchild", "GS@running-grandchild"]
                total = {}
                for n in names:
                    for model, t in executions[n]["tokens_by_model"].items():
                        b = total.setdefault(model, {})
                        for k in FIELDS + ["thinking_tokens", "total_tokens"]:
                            b[k] = b.get(k, 0) + t[k]
                check(total == case[field]["by_model"], f"{case['id']}: {field} subtotal")
    # Snapshot relations: r1 is a prefix of the later T transcript; running snapshots are prefixes too.
    for snap, final in [("r1", "T.A"), ("running", "S.C"), ("running-grandchild", "S.B")]:
        later = read(RECORDS / executions[final]["file"])
        early = read(RECORDS / executions[final]["file"].replace("default/", snap + "/", 1))
        check(later[:len(early)] == early, f"{snap}: snapshot is not a prefix of the final transcript")
    check(executions["S.C@running"]["tokens_total"]["total_tokens"] < executions["S.C"]["tokens_total"]["total_tokens"], "Interruption snapshot should retain later usage")
    check(executions["S.B@running-grandchild"]["handback_lines"] == executions["S.B"]["handback_lines"] and executions["S.B"]["end_line"] > executions["S.B"]["handback_lines"][0], "Handback precedes the final stop")
    verify_streams(oracle)
    verify_variants(oracle)
    print(f"Verified {len(inventory)} native files, {len(executions)} executions, independent arithmetic, references, clocks and privacy.")


def verify_streams(oracle):
    events = {name: read(RECORDS / "events" / f"{name}-events.jsonl") for name in ("l2", "r7")}
    transcript = {}
    for p in (RECORDS / "override").rglob("*.jsonl"):
        for r in read(p):
            if r["type"] == "assistant":
                transcript[r["message"]["id"]] = r["message"]["usage"]
    streamed = [r for r in events["l2"] if r["type"] == "assistant"]
    check(any(r["message"]["usage"]["output_tokens"] != transcript[r["message"]["id"]]["output_tokens"] for r in streamed), "Stream usage should be a non-final snapshot")
    check(set(transcript) - {r["message"]["id"] for r in streamed}, "A persisted message is absent from the stream")
    r7 = events["r7"]
    note = next(r for r in r7 if r.get("subtype") == "task_notification")
    check(note["status"] == "completed" and note["usage"]["duration_ms"] == 4258, "R7 child stop notification")
    check(not list((RECORDS / "ephemeral").rglob("*.jsonl")) and list((RECORDS / "ephemeral").rglob("*.meta.json")), "R7: only meta.json persisted")


def verify_variants(oracle):
    recipes = json.loads((ROOT / "variants.json").read_text())
    source_hashes = {p: hashlib.sha256(p.read_bytes()).hexdigest() for p in RECORDS.rglob("*") if p.is_file()}
    with tempfile.TemporaryDirectory(prefix="claude-fixture-check-") as scratch:
        output = Path(scratch) / "derived"
        hashes = prepare(output)
        for recipe in recipes["variants"]:
            e = oracle["executions"][recipe["execution"]]
            file = output / recipe["name"] / Path(*Path(e["file"]).parts[1:])
            parent = output / recipe["name"] / Path(*Path(oracle["files"][recipe["base"] + ":P"]["file"]).parts[1:])
            op = recipe["operation"]
            if op == "remove-file":
                check(not file.exists() and file.with_name(file.name.replace(".jsonl", ".meta.json")).exists(), "Missing-descendant recipe")
                continue
            if op == "truncate-tail":
                lines = file.read_text().splitlines()
                for line in lines[:-1]:
                    json.loads(line)
                try:
                    json.loads(lines[-1])
                except json.JSONDecodeError:
                    pass
                else:
                    raise AssertionError("Truncation recipe has no malformed tail")
                continue
            rows = read(file)
            mine = [r for r in rows if r.get("agentId") == e["agent_id"]]
            if op == "remove-start":
                check(rows[0]["type"] != "user" or rows[0]["timestamp"] != e["started_at"], "Start still present")
            elif op == "remove-end":
                check(all(r["timestamp"] != e["ended_at"] for r in rows), "Terminal still present")
            elif op == "remove-notification":
                check(not any(f"<task-id>{e['agent_id']}</task-id>" in (r.get("content") or r.get("attachment", {}).get("prompt") or "") for r in read(parent)), "Stop notification still present")
            elif op == "remove-usage":
                check(not any("usage" in r["message"] for r in mine if r["type"] == "assistant"), "Usage still present")
            elif op == "remove-model":
                check(not any("model" in r.get("message", {}) or "requestedModel" in r or "model" in r.get("params", {}) for r in rows), "Model still present")
                check(not any("modelId" in r.get("attachment", {}).get("identity", {}) for r in rows), "Target model attachment still present")
                check(not any("resolvedModel" in r.get("toolUseResult", {}) for r in read(parent) if r.get("toolUseResult", {}).get("agentId") == e["agent_id"]), "Spawn resolvedModel still present")
            elif op == "duplicate-row":
                ids = [r["message"]["id"] for r in rows if r["type"] == "assistant"]
                check(len(ids) == len(e["usage_lines"]) + 1 and set(ids) == set(e["message_ids"]), "Duplicate recipe changed identities")
            elif op == "remove-prior-invocation":
                check(sum((r.get("origin") or {}).get("kind") == "coordinator" for r in rows) == 2 and rows[0].get("origin", {}).get("kind") == "coordinator", "Prior invocation still present")
            elif op == "reverse-end":
                check(rows[e["end_line"] - 1]["timestamp"] < e["started_at"], "End was not reversed")
            elif op == "append-unknown":
                check(rows[-1]["type"] == "fixture_unknown_record", "Unknown record missing")
            elif op == "append-sentinel":
                check("FIXTURE_BODY_MUST_NOT_LEAK" in file.read_text(), "Synthetic body marker missing")
        lookup = output / "lookup"
        check(not (lookup / "nonexistent").exists() and not list((lookup / "empty").iterdir()), "Nonexistent/empty controls")
        target = oracle["agents"]["T"]
        for root_name, expected_count in [("explicit", 1), ("default", 3)]:
            matches = list((lookup / root_name).rglob(f"agent-{target}.jsonl"))
            check(len(matches) == 1, f"L3 {root_name}: requested target missing or duplicated")
            check(1 + sum((r.get("origin") or {}).get("kind") == "coordinator" for r in read(matches[0])) == expected_count, f"L3 {root_name}: invocation contrast lost")
        check(not list((lookup / "configured").rglob(f"agent-{target}.jsonl")) and list((lookup / "configured").rglob(f"agent-{oracle['agents']['O.child']}.jsonl")), "L3 configured root is not the unrelated override capture")
        check(not list((lookup / "no-target").rglob(f"agent-{target}.*")), "Wrong-ID control contains requested target")
        check(recipes["lookup_cases"][0]["requested_agent"] == target and recipes["lookup_cases"][0]["execution"] is None, "L3 must expose ambiguity without an execution selector")
        try:
            prepare(output)
        except ValueError:
            pass
        else:
            raise AssertionError("Existing output was overwritten")
        check(all(hashlib.sha256((output / f).read_bytes()).hexdigest() == d for f, d in hashes.items()), "Generated files changed during validation")
    check(all(hashlib.sha256(p.read_bytes()).hexdigest() == d for p, d in source_hashes.items()), "Preparation changed original fixtures")


if __name__ == "__main__":
    verify()
