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
COUNTERS = "input_tokens cached_input_tokens cache_write_input_tokens output_tokens reasoning_output_tokens total_tokens".split()


def read(path):
    return [json.loads(line) for line in path.read_text().splitlines()]


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def privacy(value):
    forbidden = {"cwd", "git", "creator_user_id", "creator_account_id", "base_instructions", "encrypted_content", "rate_limits", "internal_chat_message_metadata_passthrough", "content", "tools", "command", "aggregated_output"}
    if isinstance(value, dict):
        check(not forbidden.intersection(value), "Private/body field retained")
        for key, child in value.items():
            if key in {"message", "prompt", "text", "last_agent_message"}:
                check(child in ("", None), "Body must be empty or native null")
            if key in {"arguments", "output"}:
                check(isinstance(child, str), "Native embedded JSON must stay a string")
                if child:
                    embedded = json.loads(child)
                    allowed = {"task_name", "target", "fork_turns", "timeout_ms", "message"} if key == "arguments" else {"task_name", "previous_status"}
                    check(set(embedded) <= allowed, "Unexpected embedded tool content")
                    privacy(embedded)
            privacy(child)
    elif isinstance(value, list):
        for child in value:
            privacy(child)
    elif isinstance(value, str):
        check(not re.search(r"/Users/|/home/|/private/|/var/folders/|https?://|Bearer |sk-[A-Za-z0-9]|[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}", value), "Private path, credential or external address retained")


def verify():
    inventory = json.loads((RECORDS / "inventory.json").read_text())
    check({i["file"] for i in inventory} == {str(p.relative_to(RECORDS)) for p in RECORDS.rglob("*.jsonl")}, "Inventory/file set mismatch")
    for item in inventory:
        path = RECORDS / item["file"]
        check(hashlib.sha256(path.read_bytes()).hexdigest() == item["sha256"], f"Fixture changed: {item['file']}")
        rows = read(path)
        check(len(rows) == len(item["source_lines"]), "Source-line mapping lost")
        for row in rows:
            privacy(row)

    oracle = json.loads((ROOT / "expected.json").read_text())
    executions = oracle["executions"]
    for name, expected in executions.items():
        rows = read(RECORDS / expected["file"])
        start = rows[expected["start_line"] - 1]
        check(start["payload"]["type"] == "task_started", f"{name}: missing start")
        check(start["payload"]["turn_id"] == expected["turn_id"], f"{name}: wrong execution")
        check(start["timestamp"] == expected["started_at"], f"{name}: start time")
        check(start["payload"]["turn_attribution"]["parent_turn_id"] == expected["parent_turn_id"], f"{name}: wrong parent turn")
        check(rows[0]["payload"]["id"] == expected["thread_id"], f"{name}: wrong thread")
        check(rows[0]["payload"]["model_provider"] == expected["model"]["provider"], f"{name}: provider")
        models = [r["payload"]["model"] for r in rows if r["type"] == "turn_context" and r["payload"]["turn_id"] == expected["turn_id"]]
        check(models == [expected["model"]["model"]], f"{name}: model attribution")
        usage = [(i, r["payload"]) for i, r in enumerate(rows, 1) if r["type"] == "token_usage_record" and r["payload"]["turn_id"] == expected["turn_id"]]
        check([i for i, _ in usage] == expected["usage_lines"], f"{name}: usage evidence lines")
        ids = [p["response_id"] for _, p in usage]
        check(ids == expected["response_ids"] and len(set(ids)) == len(ids), f"{name}: response identities")
        observed = {k: sum(p["usage"][k] for _, p in usage) for k in COUNTERS}
        check(observed == expected["tokens"], f"{name}: independent usage sum")
        prior = [r["payload"]["thread_token_usage"] for r in rows[:expected["start_line"] - 1] if r["type"] == "token_usage_record"]
        baseline = prior[-1] if prior else dict.fromkeys(COUNTERS, 0)
        check(baseline == expected["baseline"], f"{name}: nonzero baseline lost")
        last = usage[-1][1]
        check(last["thread_token_usage"] == expected["final_thread_tokens"], f"{name}: thread cumulative")
        check(last["turn_token_usage"] == expected["final_turn_tokens"] == observed, f"{name}: turn cumulative")
        check({k: last["thread_token_usage"][k] - baseline[k] for k in COUNTERS} == observed, f"{name}: cumulative delta")
        check(observed["total_tokens"] == observed["input_tokens"] + observed["output_tokens"], f"{name}: subset double counting")
        terminals = [r for r in rows if r["payload"].get("type") in {"task_complete", "turn_aborted"} and r["payload"].get("turn_id") == expected["turn_id"]]
        if expected["end_line"] is None:
            check(not terminals and expected["elapsed_ms"] is None and not expected["usage_final"], f"{name}: running is not complete")
        else:
            end = rows[expected["end_line"] - 1]
            check(terminals == [end], f"{name}: terminal boundary")
            check(end["timestamp"] == expected["ended_at"], f"{name}: end time")
            state = "interrupted" if end["payload"]["type"] == "turn_aborted" else "completed"
            check(state == expected["status"] and expected["usage_final"], f"{name}: state")
            dt = lambda value: datetime.fromisoformat(value.replace("Z", "+00:00"))
            elapsed = round((dt(end["timestamp"]) - dt(start["timestamp"])).total_seconds() * 1000)
            check(elapsed == expected["elapsed_ms"], f"{name}: wall-clock interval")
            check(end["payload"]["duration_ms"] == expected["native_duration_ms"], f"{name}: native duration")
            check((end["payload"]["completed_at"] - end["payload"]["started_at"]) * 1000 == expected["epoch_elapsed_ms"], f"{name}: epoch interval")

    links = [(executions[p]["file"], executions[p]["thread_id"], executions[p]["turn_id"], c) for p, c in [("T.B", "G.B"), ("T.C", "G.C.running")]]
    for parent_file, child_name in [("default:/root", "T.A"), ("default:/root", "S.A"), ("override:/root", "O.child")]:
        parent = oracle["files"][parent_file]
        links.append((parent["file"], parent["thread_id"], executions[child_name]["parent_turn_id"], child_name))
    for parent_file, parent_thread, parent_turn, child_name in links:
        child = executions[child_name]
        rows = read(RECORDS / parent_file)
        meta = read(RECORDS / child["file"])[0]["payload"]
        check(meta["parent_thread_id"] == parent_thread and child["parent_turn_id"] == parent_turn, "Child execution lineage")
        activity = next(r["payload"]["item"] for r in rows if r["payload"].get("turn_id") == parent_turn and r["payload"].get("item", {}).get("agent_thread_id") == child["thread_id"] and r["payload"]["item"]["kind"] == "started")
        call = next(r["payload"] for r in rows if r["payload"].get("type") == "function_call" and r["payload"]["call_id"] == activity["id"])
        returned = next(r["payload"] for r in rows if r["payload"].get("type") == "function_call_output" and r["payload"]["call_id"] == call["call_id"])
        check(json.loads(returned["output"])["task_name"] == activity["agent_path"] == child["agent_reference"], "Returned reference/thread bridge")
    for case in oracle["cases"]:
        for field in ("subtree_tokens", "known_subtree_tokens"):
            if field in case:
                check({k: sum(executions[n]["tokens"][k] for n in case["included"]) for k in COUNTERS} == case[field], f"{case['id']}: subtree subtotal")
    # Snapshots retain identical past execution records, while later records differ.
    tfile = Path(executions["T.A"]["file"])
    check(read(RECORDS / "r1" / Path(*tfile.parts[1:])) == read(RECORDS / tfile)[:9], "R1 snapshot is not the original prefix")
    check(executions["S.running"]["tokens"]["total_tokens"] < executions["S.interrupted"]["tokens"]["total_tokens"], "Interruption snapshot should retain later observed usage")
    verify_variants(oracle)
    print(f"Verified {len(inventory)} native files, {len(executions)} executions, independent arithmetic, references, clocks and privacy.")


def verify_variants(oracle):
    recipes = json.loads((ROOT / "variants.json").read_text())["variants"]
    source_hashes = {p: hashlib.sha256(p.read_bytes()).hexdigest() for p in RECORDS.rglob("*.jsonl")}
    with tempfile.TemporaryDirectory(prefix="codex-fixture-check-") as scratch:
        output = Path(scratch) / "derived"
        hashes = prepare(output)
        for recipe in recipes:
            expected = oracle["executions"][recipe["execution"]]
            file = output / recipe["name"] / Path(*Path(expected["file"]).parts[1:])
            op = recipe["operation"]
            if op == "remove-file":
                check(not file.exists(), "Missing-descendant recipe kept descendant")
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
            types = [r["payload"].get("type") for r in rows]
            if op == "remove-start":
                check("task_started" not in types, "Start still present")
            elif op == "remove-end":
                check("task_complete" not in types, "Terminal still present")
            elif op == "remove-usage":
                check(not any(r["type"] == "token_usage_record" for r in rows) and "token_count" not in types, "Redundant usage evidence remains")
            elif op == "remove-prior-usage":
                start = next(i for i, r in enumerate(rows) if r["payload"].get("type") == "task_started" and r["payload"]["turn_id"] == expected["turn_id"])
                check(not any(r["type"] == "token_usage_record" or r["payload"].get("type") == "token_count" for r in rows[:start]), "Prior baseline evidence remains")
                check(sum(r["payload"]["usage"]["total_tokens"] for r in rows if r["type"] == "token_usage_record" and r["payload"]["turn_id"] == expected["turn_id"]) == 82419, "Independent B increments lost")
            elif op == "duplicate-response":
                usage = [r["payload"] for r in rows if r["type"] == "token_usage_record"]
                unique = {p["response_id"]: p["usage"]["total_tokens"] for p in usage}
                check(len(usage) == len(unique) + 1 and sum(unique.values()) == 40080, "Duplicate recipe changed independent consumption")
            elif op == "remove-model":
                check(all("model" not in r["payload"] for r in rows if r["type"] == "turn_context"), "Target model still present")
                for p in file.parents[3].rglob("*.jsonl"):
                    check(all("model" not in r["payload"].get("item", {}) for r in read(p) if r["payload"].get("item", {}).get("agent_thread_id") == expected["thread_id"]), "Spawn model still present")
            elif op == "reverse-end":
                check(rows[expected["end_line"] - 1]["timestamp"] < expected["started_at"], "End was not reversed")
            elif op == "append-unknown":
                check(rows[-1]["type"] == "fixture_unknown_record", "Unknown record missing")
            elif op == "append-sentinel":
                check("FIXTURE_BODY_MUST_NOT_LEAK" in file.read_text(), "Synthetic body marker missing")
        check(not (output / "lookup/nonexistent").exists(), "Nonexistent control exists")
        check(not list((output / "lookup/empty").iterdir()), "Empty control is not empty")
        target_id = oracle["executions"]["T.A"]["thread_id"]
        for root_name, expected_count in [("explicit", 1), ("default", 3)]:
            matches = [read(p) for p in (output / "lookup" / root_name).rglob("*.jsonl") if read(p)[0]["payload"]["id"] == target_id]
            check(len(matches) == 1, f"L3 {root_name}: requested target missing or duplicated")
            check(sum(r["payload"].get("type") == "task_started" for r in matches[0]) == expected_count, f"L3 {root_name}: invocation contrast lost")
        configured_ids = {read(p)[0]["payload"]["id"] for p in (output / "lookup/configured").rglob("*.jsonl")}
        check(configured_ids == {value["thread_id"] for name, value in oracle["files"].items() if name.startswith("override:")}, "L3 configured root is not the unrelated real override capture")
        lookup_case = json.loads((ROOT / "variants.json").read_text())["lookup_cases"][0]
        check(lookup_case["requested_agent_reference"] == "/root/capture_target" and lookup_case["execution"] is None, "L3 must expose fallback ambiguity without an execution selector")
        check(all(read(p)[0]["payload"]["id"] != target_id for p in (output / "lookup/no-target").rglob("*.jsonl")), "Wrong-ID control contains requested target")
        try:
            prepare(output)
        except ValueError:
            pass
        else:
            raise AssertionError("Existing output was overwritten")
        check(all(hashlib.sha256((output / file).read_bytes()).hexdigest() == digest for file, digest in hashes.items()), "Generated files changed during validation")
    check(all(hashlib.sha256(p.read_bytes()).hexdigest() == digest for p, digest in source_hashes.items()), "Preparation changed original fixtures")


if __name__ == "__main__":
    verify()
