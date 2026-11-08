#!/usr/bin/env python3
"""Verify frozen OpenCode V2 fixture evidence without agent-metrics code."""

import hashlib
import importlib.util
import json
import re
import sqlite3
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parent
RECORDS = ROOT / "records"
COUNTERS = (
    "input_tokens",
    "output_tokens",
    "reasoning_tokens",
    "cache_read_tokens",
    "cache_write_tokens",
)
TOKEN_PATHS = {
    "input_tokens": ("input",),
    "output_tokens": ("output",),
    "reasoning_tokens": ("reasoning",),
    "cache_read_tokens": ("cache", "read"),
    "cache_write_tokens": ("cache", "write"),
}
TABLE_SQL_SHA256 = {
    "project": "386060bdb1aa31523f5e6ff622df6415f6c7426fbd2a27a0cce54562b25ce538",
    "session_message": "656245ee15a2d7f70ac64e1db3970af45fb813e70505a0e97d7f3928cc577570",
    "session_v2": "cf50dab7c4993711be74d0859a2ffd39191dc8f80e5b5b32f145de198826776e",
}
INDEXES = {
    "project": {
        "sqlite_autoindex_project_1": (1, "pk", 0, ("id",), None),
    },
    "session_v2": {
        "sqlite_autoindex_session_v2_1": (1, "pk", 0, ("id",), None),
        "session_v2_project_idx": (
            0, "c", 0, ("project_id",),
            "CREATE INDEX session_v2_project_idx ON session_v2(project_id)",
        ),
        "session_v2_workspace_idx": (
            0, "c", 0, ("workspace_id",),
            "CREATE INDEX session_v2_workspace_idx ON session_v2(workspace_id)",
        ),
        "session_v2_parent_idx": (
            0, "c", 0, ("parent_id",),
            "CREATE INDEX session_v2_parent_idx ON session_v2(parent_id)",
        ),
        "session_v2_time_suspended_idx": (
            0, "c", 1, ("time_suspended",),
            "CREATE INDEX session_v2_time_suspended_idx ON session_v2(time_suspended) WHERE time_suspended IS NOT NULL",
        ),
    },
    "session_message": {
        "sqlite_autoindex_session_message_1": (1, "pk", 0, ("id",), None),
        "session_message_session_seq_idx": (
            1, "c", 0, ("session_id", "seq"),
            "CREATE UNIQUE INDEX session_message_session_seq_idx ON session_message(session_id, seq)",
        ),
        "session_message_session_type_seq_idx": (
            0, "c", 0, ("session_id", "type", "seq"),
            "CREATE INDEX session_message_session_type_seq_idx ON session_message(session_id, type, seq)",
        ),
        "session_message_session_time_created_id_idx": (
            0, "c", 0, ("session_id", "time_created", "id"),
            "CREATE INDEX session_message_session_time_created_id_idx ON session_message(session_id, time_created, id)",
        ),
        "session_message_time_created_idx": (
            0, "c", 0, ("time_created",),
            "CREATE INDEX session_message_time_created_idx ON session_message(time_created)",
        ),
    },
}
SECRET = re.compile(
    rb"/Users/|/home/|/private/|/var/folders/|/tmp/|https?://|Bearer |"
    rb"sk-[A-Za-z0-9]|[\w.+-]+@[\w-]+\.[A-Za-z]{2,}"
)
FORBIDDEN_JSON_KEYS = {
    "snapshot",
    "reasoningEncryptedContent",
    "files",
    "agents",
    "outputPath",
    "toolCalls",
    "url",
    "questions",
    "answers",
}
BODY_KEYS = {"text", "prompt", "command", "workdir", "path", "code"}
FORBIDDEN_METADATA_KEYS = {"prompt", "message", "body", "reasoning", "command", "tool_input", "tool_output"}
SOURCE_LABELS = {
    "r1.db": "snapshot:r1",
    "default.db": "snapshot:default",
    "running.db": "snapshot:running",
    "interrupted.db": "snapshot:interrupted",
    "override.db": "snapshot:override",
    "api/running-active.json": "api:running-active",
    "api/interrupt-running-active.json": "api:interrupt-running-active",
    "api/interrupt-response.json": "api:interrupt-response",
    "api/interrupted-active.json": "api:interrupted-active",
}


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def read_json(path):
    return json.loads(path.read_text())


def connect(path):
    return sqlite3.connect(f"file:{path}?mode=ro&immutable=1", uri=True)


def privacy(value, key=""):
    if isinstance(value, dict):
        unexpected = FORBIDDEN_JSON_KEYS.intersection(value)
        check(not unexpected, f"Private/body keys retained: {sorted(unexpected)}")
        for child_key, child in value.items():
            if child_key in BODY_KEYS:
                check(child == "", f"Body field is not empty: {child_key}")
            privacy(child, child_key)
    elif isinstance(value, list):
        for child in value:
            privacy(child, key)
    elif isinstance(value, str):
        check(not SECRET.search(value.encode()), f"Private path, credential, or address retained in {key}")


def metadata_privacy(value, key=""):
    if isinstance(value, dict):
        unexpected = FORBIDDEN_METADATA_KEYS.intersection(value)
        check(not unexpected, f"Body keys retained in public metadata: {sorted(unexpected)}")
        for child_key, child in value.items():
            metadata_privacy(child, child_key)
    elif isinstance(value, list):
        for child in value:
            metadata_privacy(child, key)
    elif isinstance(value, str):
        check(not SECRET.search(value.encode()), f"Private path, credential, or address retained in metadata {key}")


def message_clocks(value):
    clocks = []
    if isinstance(value, dict):
        for key, child in value.items():
            if key == "time":
                check(isinstance(child, dict), "Message time is not an object")
                check(
                    all(isinstance(clock, int) and not isinstance(clock, bool) for clock in child.values()),
                    "Message clock is not an integer",
                )
                clocks.extend(child.values())
            clocks.extend(message_clocks(child))
    elif isinstance(value, list):
        for child in value:
            clocks.extend(message_clocks(child))
    return clocks


def message_shape(kind, data):
    allowed = {
        "user": {"time", "text"},
        "assistant": {"time", "agent", "model", "content", "finish", "native", "cost", "tokens"},
        "idle": {"time", "outcome"},
    }
    check(kind in allowed, f"Unexpected message type: {kind}")
    check(set(data) <= allowed[kind], f"Unexpected {kind} fields: {set(data) - allowed[kind]}")
    if kind == "assistant":
        check(set(data.get("model", {})) <= {"id", "providerID", "variant"}, "Unexpected model field")
        check(set(data.get("native", {})) <= {"responseId", "serviceTier"}, "Unexpected native field")
        tokens = data.get("tokens")
        if tokens is not None:
            check(set(tokens) == {"input", "output", "reasoning", "cache"}, "Unexpected token shape")
            check(set(tokens["cache"]) == {"read", "write"}, "Unexpected cache shape")
            check(all(type(tokens[k]) is int for k in ("input", "output", "reasoning")), "Non-integer token counter")
            check(all(type(tokens["cache"][k]) is int for k in ("read", "write")), "Non-integer cache counter")
        for part in data.get("content", []):
            part_type = part.get("type")
            if part_type in {"reasoning", "text"}:
                check(set(part) <= {"type", "text", "time"}, f"Unexpected {part_type} part field")
                check(part.get("text") == "", f"{part_type} body retained")
            elif part_type == "tool":
                check(set(part) <= {"type", "id", "name", "executed", "state", "time"}, "Unexpected tool part field")
                state = part.get("state", {})
                check(set(state) <= {"status", "input", "metadata"}, "Unexpected tool state field")
                check(set(state.get("metadata", {})) <= {"sessionID"}, "Unexpected tool metadata field")
                allowed_input = {
                    "subagent": {"agent", "background", "sessionID", "prompt"},
                    "shell": {"timeout", "background", "command", "workdir"},
                    "read": {"path", "offset", "limit"},
                }.get(part.get("name"), set())
                check(set(state.get("input", {})) <= allowed_input, f"Unexpected {part.get('name')} input field")
            else:
                raise AssertionError(f"Unexpected content part type: {part_type}")
    privacy(data)


def inventory_rows(db):
    projects = [row[0] for row in db.execute("SELECT id FROM project ORDER BY id")]
    sessions = [row[0] for row in db.execute("SELECT id FROM session_v2 ORDER BY id")]
    messages = {}
    for session_id, seq in db.execute("SELECT session_id, seq FROM session_message ORDER BY session_id, seq"):
        messages.setdefault(session_id, []).append(seq)
    return {"project": projects, "session_v2": sessions, "session_message": messages}


def verify_files():
    inventory = read_json(RECORDS / "inventory.json")
    metadata_privacy(inventory)
    entries = inventory["files"]
    actual = {
        str(path.relative_to(RECORDS))
        for path in RECORDS.rglob("*")
        if path.is_file() and path.name != "inventory.json"
    }
    check({entry["file"] for entry in entries} == actual, "Inventory/file set mismatch")
    for entry in entries:
        path = RECORDS / entry["file"]
        check(entry["classification"] == "real-derived", f"Wrong classification: {entry['file']}")
        check(entry.get("source") == SOURCE_LABELS[entry["file"]], f"Unsafe source label: {entry['file']}")
        check(digest(path) == entry["sha256"], f"Fixture changed: {entry['file']}")
        check(not SECRET.search(path.read_bytes()), f"Private bytes retained: {entry['file']}")
        if path.suffix != ".db":
            privacy(read_json(path))
            continue
        check(not path.with_name(path.name + "-wal").exists(), f"WAL distributed: {entry['file']}")
        check(not path.with_name(path.name + "-shm").exists(), f"SHM distributed: {entry['file']}")
        with connect(path) as db:
            check(db.execute("PRAGMA integrity_check").fetchone() == ("ok",), f"Integrity failure: {entry['file']}")
            check(db.execute("PRAGMA foreign_key_check").fetchall() == [], f"Foreign-key failure: {entry['file']}")
            check(db.execute("PRAGMA freelist_count").fetchone() == (0,), f"Freelist retained: {entry['file']}")
            tables = {
                row[0]
                for row in db.execute("SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'")
            }
            check(tables == set(TABLE_SQL_SHA256), f"Unexpected tables in {entry['file']}: {tables}")
            extra_objects = db.execute(
                "SELECT type, name FROM sqlite_schema WHERE type IN ('view', 'trigger') ORDER BY type, name"
            ).fetchall()
            check(extra_objects == [], f"Unexpected views or triggers in {entry['file']}: {extra_objects}")
            for table, expected_sql_hash in TABLE_SQL_SHA256.items():
                table_sql = db.execute(
                    "SELECT sql FROM sqlite_schema WHERE type='table' AND name=?", (table,)
                ).fetchone()[0]
                check(
                    hashlib.sha256(table_sql.encode()).hexdigest() == expected_sql_hash,
                    f"Table SQL drift in {entry['file']}:{table}",
                )
                indexes = {}
                for _, name, unique, origin, partial in db.execute(f"PRAGMA index_list({table})"):
                    columns = tuple(row[2] for row in db.execute(f"PRAGMA index_info({name})"))
                    sql = db.execute(
                        "SELECT sql FROM sqlite_schema WHERE type='index' AND name=?", (name,)
                    ).fetchone()[0]
                    indexes[name] = (unique, origin, partial, columns, sql)
                check(indexes == INDEXES[table], f"Index schema drift in {entry['file']}:{table}")
            check(inventory_rows(db) == entry["rows"], f"Source-row map mismatch: {entry['file']}")
            for kind, time_created, time_updated, raw in db.execute(
                "SELECT type, time_created, time_updated, data FROM session_message ORDER BY session_id, seq"
            ):
                data = json.loads(raw)
                message_shape(kind, data)
                check(time_created == data["time"]["created"], "SQL and JSON message creation clocks differ")
                clocks = message_clocks(data)
                check(all(time_created <= clock <= time_updated for clock in clocks), "JSON clock outside SQL envelope")
    return inventory


def snapshot_path(oracle, snapshot):
    return RECORDS / oracle["snapshots"][snapshot]["file"]


def session_id(oracle, snapshot, role):
    return oracle["snapshots"][snapshot]["sessions"][role]


def get_session(db, value):
    row = db.execute(
        "SELECT id, project_id, parent_id, agent, model, version, cost, "
        "tokens_input, tokens_output, tokens_reasoning, tokens_cache_read, tokens_cache_write, "
        "time_created, time_updated, time_idle, idle_outcome, time_busy "
        "FROM session_v2 WHERE id=?",
        (value,),
    ).fetchone()
    check(row is not None, f"Missing session: {value}")
    keys = (
        "id", "project_id", "parent_id", "agent", "model", "version", "cost",
        "tokens_input", "tokens_output", "tokens_reasoning", "tokens_cache_read", "tokens_cache_write",
        "time_created", "time_updated", "time_idle", "idle_outcome", "time_busy",
    )
    return dict(zip(keys, row))


def messages(db, value):
    return {
        seq: {"id": message_id, "type": kind, "data": json.loads(data)}
        for message_id, seq, kind, data in db.execute(
            "SELECT id, seq, type, data FROM session_message WHERE session_id=? ORDER BY seq", (value,)
        )
    }


def nested(value, path):
    for key in path:
        value = value[key]
    return value


def usage_of(message):
    tokens = message["data"].get("tokens")
    if tokens is None:
        return None
    return {name: nested(tokens, path) for name, path in TOKEN_PATHS.items()}


def zero_tokens():
    return dict.fromkeys(COUNTERS, 0)


def add_tokens(values):
    total = zero_tokens()
    for value in values:
        for name in COUNTERS:
            total[name] += value[name]
    return {**total, "total_tokens": None}


def usage_identity(message):
    response_id = message["data"].get("native", {}).get("responseId")
    check(response_id is None or isinstance(response_id, str), "Invalid native response identity")
    return response_id or message["id"]


def add_message_usage(messages):
    unique = {}
    for message in messages:
        usage = usage_of(message)
        check(usage is not None, "Message has no usage")
        identity = usage_identity(message)
        evidence = (usage, message["data"].get("model"))
        if identity in unique:
            check(unique[identity] == evidence, f"Conflicting duplicate response: {identity}")
            continue
        unique[identity] = evidence
    return add_tokens([usage for usage, _model in unique.values()])


def counter_shape(value, label):
    check(set(value) == {*COUNTERS, "total_tokens"}, f"{label}: counter fields")
    check(all(type(value[name]) is int for name in COUNTERS), f"{label}: non-integer counter")
    check(value["total_tokens"] is None, f"{label}: native total must be null")


def verify_execution(name, expected, oracle):
    path = snapshot_path(oracle, expected["snapshot"])
    target = session_id(oracle, expected["snapshot"], expected["role"])
    with connect(path) as db:
        session = get_session(db, target)
        rows = messages(db, target)
    user = rows[expected["user_seq"]]
    check(user["type"] == "user" and user["id"] == expected["user_message_id"], f"{name}: start record")
    check(user["data"]["time"]["created"] == expected["started_at"], f"{name}: start time")
    user_seqs = [seq for seq, row in rows.items() if row["type"] == "user"]
    next_starts = [seq for seq in user_seqs if seq > expected["user_seq"]]
    segment_end = min(next_starts) - 1 if next_starts else max(rows)
    check(expected["segment"] == [expected["user_seq"], segment_end], f"{name}: invocation boundary")
    assistant = [rows[seq] for seq in expected["assistant_seqs"]]
    check(all(row["type"] == "assistant" for row in assistant), f"{name}: assistant evidence")
    check([row["id"] for row in assistant] == expected["assistant_message_ids"], f"{name}: message identities")
    usage_rows = [rows[seq] for seq in expected["usage_seqs"]]
    observed_usage_seqs = [
        seq for seq, row in rows.items()
        if expected["user_seq"] <= seq <= segment_end
        and row["type"] == "assistant"
        and usage_of(row) is not None
    ]
    check(observed_usage_seqs == expected["usage_seqs"], f"{name}: usage evidence rows")
    check(all(usage_of(row) is not None for row in usage_rows), f"{name}: missing expected usage")
    observed = add_message_usage(usage_rows)
    counter_shape(expected["tokens"], f"{name}: expected usage")
    check(observed == expected["tokens"], f"{name}: independent usage sum")
    check(expected["tokens"]["total_tokens"] is None, f"{name}: native total was fabricated")
    models = [row["data"]["model"] for row in usage_rows]
    check(all(model == expected["model"] for model in models), f"{name}: model attribution")
    check(json.loads(session["model"]) == expected["model"], f"{name}: session model")
    cumulative = {
        "input_tokens": session["tokens_input"],
        "output_tokens": session["tokens_output"],
        "reasoning_tokens": session["tokens_reasoning"],
        "cache_read_tokens": session["tokens_cache_read"],
        "cache_write_tokens": session["tokens_cache_write"],
        "total_tokens": None,
    }
    counter_shape(expected["session_cumulative"], f"{name}: expected cumulative")
    check(cumulative == expected["session_cumulative"], f"{name}: session cumulative counters")
    idle_seqs = [
        seq for seq, row in rows.items()
        if expected["user_seq"] <= seq <= segment_end and row["type"] == "idle"
    ]
    if expected["idle_seq"] is None:
        check(idle_seqs == [], f"{name}: running has terminal")
        check(expected["ended_at"] is None and expected["elapsed_ms"] is None, f"{name}: running elapsed")
        check(session["idle_outcome"] is None and session["time_idle"] is None, f"{name}: running session marked idle")
        open_tool = expected["open_tool"]
        check(open_tool["assistant_seq"] == segment_end, f"{name}: open tool is not at invocation tail")
        part = rows[open_tool["assistant_seq"]]["data"]["content"][open_tool["content_index"]]
        check(part["type"] == "tool" and part["name"] == open_tool["name"], f"{name}: open tool identity")
        check(part["state"]["status"] == open_tool["status"], f"{name}: open tool state")
    else:
        check(idle_seqs == [expected["idle_seq"]], f"{name}: idle is not first and unique")
        check(expected["idle_seq"] == segment_end, f"{name}: idle is not the invocation terminal")
        idle = rows[expected["idle_seq"]]
        check(idle["type"] == "idle" and idle["id"] == expected["idle_message_id"], f"{name}: terminal record")
        check(idle["data"]["outcome"] == expected["status"], f"{name}: outcome")
        check(idle["data"]["time"]["created"] == expected["ended_at"], f"{name}: end time")
        check(expected["ended_at"] - expected["started_at"] == expected["elapsed_ms"], f"{name}: elapsed")
        check(session["idle_outcome"] == expected["status"], f"{name}: session outcome")
        check(session["time_idle"] == expected["ended_at"], f"{name}: session idle clock")
    return observed


def find_tool(row, call_id):
    return next((part for part in row["data"].get("content", []) if part.get("id") == call_id), None)


def verify_links(oracle):
    for name, link in oracle["links"].items():
        path = snapshot_path(oracle, link["snapshot"])
        parent = session_id(oracle, link["snapshot"], link["parent_role"])
        child = session_id(oracle, link["snapshot"], link["child_role"])
        with connect(path) as db:
            parent_session = get_session(db, parent)
            child_session = get_session(db, child)
            row = messages(db, parent)[link["message_seq"]]
        tool = find_tool(row, link["call_id"])
        check(tool is not None and tool["name"] == "subagent", f"{name}: subagent call")
        check(tool["state"]["status"] == "completed", f"{name}: call status")
        check(tool["state"]["metadata"]["sessionID"] == child, f"{name}: returned session")
        check(tool["state"]["input"].get("sessionID") == (child if link["resume"] else None), f"{name}: resume reference")
        check(tool["state"]["input"].get("background") == link["background"], f"{name}: background mode")
        check(tool["time"] == link["time"], f"{name}: call clock")
        check(child_session["parent_id"] == parent_session["id"], f"{name}: session ancestry")
        child_execution = oracle["executions"][link["child_execution"]]
        check(
            tool["time"]["created"] <= tool["time"]["ran"] <= child_execution["started_at"],
            f"{name}: call-to-child start ordering",
        )
        if link["completion"] == "spawn":
            check(tool["time"]["completed"] < child_execution["started_at"], f"{name}: background completion is not child end")
        else:
            check(link["completion"] == "terminal", f"{name}: unknown completion meaning")
            check(child_execution["ended_at"] is not None, f"{name}: synchronous child has no terminal")
            check(child_execution["ended_at"] <= tool["time"]["completed"], f"{name}: synchronous call ended before child")


def verify_topology(oracle):
    expected = {snapshot: set() for snapshot in oracle["snapshots"]}
    for link in oracle["links"].values():
        snapshot = link["snapshot"]
        expected[snapshot].add((
            session_id(oracle, snapshot, link["parent_role"]),
            session_id(oracle, snapshot, link["child_role"]),
            link["message_seq"],
            link["call_id"],
        ))

    topology = {}
    for snapshot in oracle["snapshots"]:
        with connect(snapshot_path(oracle, snapshot)) as db:
            parents = dict(db.execute("SELECT id, parent_id FROM session_v2"))
            rows = db.execute(
                "SELECT session_id, seq, data FROM session_message WHERE type='assistant' ORDER BY session_id, seq"
            ).fetchall()
        observed = set()
        children = {session: set() for session in parents}
        for parent, seq, raw in rows:
            for part in json.loads(raw).get("content", []):
                if part.get("type") != "tool" or part.get("name") != "subagent":
                    continue
                child = part.get("state", {}).get("metadata", {}).get("sessionID")
                check(child in parents, f"{snapshot}: dangling subagent reference")
                check(parents[child] == parent, f"{snapshot}: subagent ancestry mismatch")
                observed.add((parent, child, seq, part.get("id")))
                children[parent].add(child)
        check(observed == expected[snapshot], f"{snapshot}: retained subagent call set")
        inbound = {child for child_set in children.values() for child in child_set}
        check(
            inbound == {session for session, parent in parents.items() if parent is not None},
            f"{snapshot}: retained child link closure",
        )
        topology[snapshot] = children
    return topology


def verify_parent_reduction(oracle):
    for snapshot, spec in oracle["snapshots"].items():
        parent = spec["sessions"]["P"]
        with connect(snapshot_path(oracle, snapshot)) as db:
            rows = messages(db, parent)
        check(rows, f"{snapshot}: parent link evidence missing")
        for row in rows.values():
            check(row["type"] == "assistant", f"{snapshot}: unrelated parent record retained")
            parts = row["data"].get("content", [])
            check(parts and all(part.get("name") == "subagent" for part in parts), f"{snapshot}: unrelated parent content retained")


def verify_r3(oracle):
    case = oracle["cases"]["R3"]
    target = session_id(oracle, "default", "T")
    check(target == case["session_id"] == session_id(oracle, "r1", "T"), "R3: reused session identity")
    with connect(snapshot_path(oracle, "default")) as db:
        rows = messages(db, target)
    choices = [seq for seq, row in rows.items() if row["type"] == "user"]
    check(choices == case["choices"], "R3: selectable invocation starts")
    check(rows[choices[1]]["id"] == case["selected_user_message_id"], "R3: selected invocation identity")
    first = oracle["executions"][case["baseline_execution"]]
    check(
        rows[choices[0]]["id"] == first["user_message_id"]
        and rows[choices[0]]["data"]["time"]["created"] == first["started_at"],
        "R3: first invocation start",
    )
    first_window = {
        seq: row for seq, row in rows.items()
        if choices[0] <= seq < choices[1]
    }
    first_idles = [seq for seq, row in first_window.items() if row["type"] == "idle"]
    check(first_idles == [first["idle_seq"]], "R3: first invocation idle is not unique")
    check(max(first_window) == first["idle_seq"], "R3: first idle is not the invocation terminal")
    first_idle = first_window[first["idle_seq"]]
    check(
        first_idle["id"] == first["idle_message_id"]
        and first_idle["data"]["outcome"] == first["status"]
        and first_idle["data"]["time"]["created"] == first["ended_at"],
        "R3: first invocation terminal evidence",
    )
    retained_baseline = add_message_usage([
        row
        for seq, row in rows.items()
        if choices[0] <= seq < choices[1] and row["type"] == "assistant" and usage_of(row) is not None
    ])
    check(retained_baseline == oracle["executions"][case["baseline_execution"]]["tokens"], "R3: retained first invocation baseline")
    baseline = oracle["executions"][case["baseline_execution"]]["session_cumulative"]
    final = oracle["executions"][case["target_execution"]]["session_cumulative"]
    delta = {
        name: final[name] - baseline[name]
        for name in COUNTERS
    }
    delta["total_tokens"] = None
    check(delta == case["delta"] == oracle["executions"][case["target_execution"]]["tokens"], "R3: cumulative delta")


def verify_cases(oracle, topology):
    executions = oracle["executions"]
    for case_name, case in oracle["cases"].items():
        if case_name == "R3":
            continue
        target_execution = executions[case["target"]]
        target = target_execution["tokens"]
        if "self" in case:
            check(target == case["self"], f"{case_name}: self")
        if "known_self" in case:
            check(target == case["known_self"], f"{case_name}: known self")
        if "included" in case:
            subtotal = add_tokens([executions[name]["tokens"] for name in case["included"]])
            check(subtotal == case["subtree"], f"{case_name}: subtree")
            snapshot = target_execution["snapshot"]
            root = session_id(oracle, snapshot, target_execution["role"])
            actual = set()
            pending = [root]
            while pending:
                current = pending.pop()
                check(current not in actual, f"{case_name}: topology cycle")
                actual.add(current)
                pending.extend(topology[snapshot][current])
            included = {
                session_id(oracle, snapshot, executions[name]["role"])
                for name in case["included"]
            }
            check(actual == included, f"{case_name}: included executions do not match native subtree")
        for excluded in case.get("excluded", []):
            snapshot = target_execution["snapshot"]
            role = executions[excluded]["role"] if excluded in executions else excluded
            excluded_session = session_id(oracle, snapshot, role)
            check(excluded_session not in actual, f"{case_name}: excluded execution is in native subtree")


def verify_api(oracle):
    for name, expected in oracle["api"].items():
        data = read_json(RECORDS / expected["file"])
        if name == "interrupt-response":
            check(data == {"interrupted": True}, "Interrupt API response")
            continue
        sessions = data["data"]
        target = session_id(oracle, expected["snapshot"], expected["role"])
        if expected["state"] is None:
            check(target not in sessions, f"{name}: terminal child remains active")
        else:
            check(sessions == {target: {"type": expected["state"]}}, f"{name}: active state")


def verify_bundle():
    inventory = verify_files()
    oracle = read_json(ROOT / "expected.json")
    metadata_privacy(oracle)
    check(inventory["version"] == oracle["version"] == 1, "Version mismatch")
    check(oracle["product"] == "opencode-v2" and oracle["native_version"] == "2.2.1", "Capture identity")
    observed_rows = {
        entry["file"]: entry["rows"]
        for entry in inventory["files"]
        if entry["file"].endswith(".db")
    }
    check(observed_rows == oracle["row_allowlist"], "Inventory rows differ from independent allowlist")
    for name, expected in oracle["executions"].items():
        verify_execution(name, expected, oracle)
    verify_parent_reduction(oracle)
    verify_links(oracle)
    topology = verify_topology(oracle)
    verify_r3(oracle)
    verify_cases(oracle, topology)
    verify_api(oracle)
    return inventory, oracle


def sqlite_schema(path):
    with connect(path) as db:
        return db.execute(
            "SELECT type, name, tbl_name, sql FROM sqlite_schema ORDER BY type, name"
        ).fetchall()


def verify_derived(oracle):
    for required in ("prepare.py", "variants.json", "capture.json", "README.md"):
        check((ROOT / required).is_file(), f"Missing derived fixture artifact: {required}")
    recipes = read_json(ROOT / "variants.json")
    capture = read_json(ROOT / "capture.json")
    metadata_privacy(recipes)
    metadata_privacy(capture)
    check(recipes["version"] == 1 and recipes["classification"] == "derived", "Derived recipe identity")
    expected_recipes = [
        ("missing-descendant", "override", "remove-session", "L2.G"),
        ("missing-start", "r1", "remove-start", "T.A@r1"),
        ("missing-end", "r1", "remove-end", "T.A@r1"),
        ("missing-model", "r1", "remove-model", "T.A@r1"),
        ("missing-usage", "r1", "remove-usage", "T.A@r1"),
        ("duplicate-response", "r1", "duplicate-response", "T.A@r1"),
        ("removed-prior-invocation", "default", "remove-prior-invocation", "T.B"),
        ("reversed-time", "r1", "reverse-end", "T.A@r1"),
        ("unsupported-record", "r1", "append-unknown", "T.A@r1"),
        ("body-sentinel", "r1", "body-sentinel", "T.A@r1"),
    ]
    observed_recipes = [
        (item["name"], item["base"], item["operation"], item["execution"])
        for item in recipes["variants"]
    ]
    check(observed_recipes == expected_recipes, "Derived recipe set")
    for recipe in recipes["variants"]:
        expected = recipe["expected"]
        metric_execution = oracle["executions"][recipe.get("target", recipe["execution"])]
        for key in ("self", "known_subtree", "known_tokens", "known_unattributed_tokens", "tokens"):
            if key in expected:
                counter_shape(expected[key], f"{recipe['name']}: {key}")
                check(expected[key] == metric_execution["tokens"], f"{recipe['name']}: expected counters")
        if expected.get("elapsed_ms") is not None:
            check(expected["elapsed_ms"] == metric_execution["elapsed_ms"], f"{recipe['name']}: expected elapsed")
    check(recipes["variants"][0]["expected"]["final_subtree"] is None, "Missing-descendant final subtree")
    check(
        {case["id"] for case in recipes["lookup_cases"]}
        == {"L3-explicit", "L3-invalid-explicit", "L4-missing-config", "L4-empty", "L4-wrong-target"},
        "Lookup recipe set",
    )
    check(capture["product"] == "opencode-v2" and capture["native_version"] == "2.2.1", "Capture ledger identity")
    check(capture["privacy"]["time_shift_days"] == -313, "Capture ledger time shift")
    check(capture["native_adapter_cli_support_claimed"] is False, "Capture ledger overclaims adapter support")
    module_spec = importlib.util.spec_from_file_location(
        "opencode_fixture_prepare", ROOT / "prepare.py"
    )
    preparer = importlib.util.module_from_spec(module_spec)
    module_spec.loader.exec_module(preparer)
    originals = {
        str(path.relative_to(RECORDS)): digest(path)
        for path in RECORDS.rglob("*")
        if path.is_file()
    }
    with tempfile.TemporaryDirectory(prefix="opencode-fixture-derived-") as scratch:
        first = Path(scratch) / "first"
        second = Path(scratch) / "second"
        first_hashes = preparer.prepare(first)
        second_hashes = preparer.prepare(second)
        check(first_hashes == second_hashes, "Derived preparation is not deterministic")
        actual_hashes = {
            str(path.relative_to(first)): digest(path)
            for path in first.rglob("*")
            if path.is_file() and path.name != "hashes.json"
        }
        check(first_hashes == actual_hashes == read_json(first / "hashes.json"), "Derived hash inventory")
        try:
            preparer.prepare(first)
        except ValueError as error:
            check(str(error) == "Use a new output directory; existing data is never replaced", "Wrong overwrite error")
        else:
            raise AssertionError("Derived preparation overwrote an existing destination")

        variants = first / "variants"
        source_schema = {name: sqlite_schema(snapshot_path(oracle, name)) for name in oracle["snapshots"]}
        for recipe in recipes["variants"]:
            path = variants / f"{recipe['name']}.db"
            with connect(path) as db:
                check(db.execute("PRAGMA integrity_check").fetchone() == ("ok",), f"{recipe['name']}: integrity")
                check(db.execute("PRAGMA foreign_key_check").fetchall() == [], f"{recipe['name']}: foreign keys")
                check(db.execute("PRAGMA freelist_count").fetchone() == (0,), f"{recipe['name']}: freelist")
            check(sqlite_schema(path) == source_schema[recipe["base"]], f"{recipe['name']}: schema drift")

        missing_descendant = variants / "missing-descendant.db"
        grandchild = session_id(oracle, "override", "G")
        delegator = session_id(oracle, "override", "T")
        with connect(missing_descendant) as db:
            check(db.execute("SELECT 1 FROM session_v2 WHERE id=?", (grandchild,)).fetchone() is None, "Missing descendant retained")
            check(db.execute("SELECT 1 FROM session_message WHERE session_id=?", (grandchild,)).fetchone() is None, "Missing descendant messages retained")
            link_row = messages(db, delegator)[oracle["links"]["L2.T-G"]["message_seq"]]
        check(find_tool(link_row, oracle["links"]["L2.T-G"]["call_id"])["state"]["metadata"]["sessionID"] == grandchild, "Missing descendant reference removed")

        target = session_id(oracle, "r1", "T")
        execution = oracle["executions"]["T.A@r1"]
        with connect(variants / "missing-start.db") as db:
            check(execution["user_seq"] not in messages(db, target), "Missing-start row retained")
        with connect(variants / "missing-end.db") as db:
            check(execution["idle_seq"] not in messages(db, target), "Missing-end row retained")
            session = get_session(db, target)
        check(session["time_idle"] == execution["ended_at"] and session["idle_outcome"] == execution["status"], "Missing-end summary was removed")
        with connect(variants / "missing-model.db") as db:
            session = get_session(db, target)
            target_rows = messages(db, target)
        check(session["model"] is None, "Missing-model session evidence retained")
        check(all("model" not in target_rows[seq]["data"] for seq in execution["assistant_seqs"]), "Missing-model message evidence retained")
        with connect(variants / "missing-usage.db") as db:
            session = get_session(db, target)
            target_rows = messages(db, target)
        check(all("tokens" not in target_rows[seq]["data"] for seq in execution["assistant_seqs"]), "Missing-usage message counters retained")
        check(session["tokens_input"] == execution["session_cumulative"]["input_tokens"], "Missing-usage cumulative counter changed")

        with connect(snapshot_path(oracle, "r1")) as db:
            original_response = messages(db, target)[execution["usage_seqs"][0]]["data"]["native"]["responseId"]
        with connect(variants / "duplicate-response.db") as db:
            duplicate_rows = messages(db, target)
            response_rows = [
                row for row in duplicate_rows.values()
                if row["data"].get("native", {}).get("responseId") == original_response
            ]
        check(len(response_rows) == 2 and len({row["id"] for row in response_rows}) == 2, "Duplicate response identity not materialized")
        duplicate_usage = add_message_usage([
            row
            for seq, row in duplicate_rows.items()
            if execution["segment"][0] <= seq <= execution["segment"][1]
            and row["type"] == "assistant"
            and usage_of(row) is not None
        ])
        duplicate_expected = next(
            recipe["expected"]["tokens"]
            for recipe in recipes["variants"]
            if recipe["name"] == "duplicate-response"
        )
        check(duplicate_usage == duplicate_expected, "Duplicate native response was counted twice")

        reused = session_id(oracle, "default", "T")
        prior = oracle["executions"][oracle["cases"]["R3"]["baseline_execution"]]
        current = oracle["executions"]["T.B"]
        with connect(variants / "removed-prior-invocation.db") as db:
            reused_rows = messages(db, reused)
            reused_session = get_session(db, reused)
        check(not any(prior["segment"][0] <= seq <= prior["segment"][1] for seq in reused_rows), "Prior invocation retained")
        check(current["user_seq"] in reused_rows, "Current reused invocation removed")
        check(reused_session["tokens_input"] == current["session_cumulative"]["input_tokens"], "Reused cumulative counter changed")

        with connect(variants / "reversed-time.db") as db:
            reversed_rows = messages(db, target)
            reversed_session = get_session(db, target)
        reversed_end = reversed_rows[execution["idle_seq"]]["data"]["time"]["created"]
        check(reversed_end < execution["started_at"] and reversed_session["time_idle"] == reversed_end, "Reversed boundary not materialized")
        with connect(variants / "unsupported-record.db") as db:
            unknown = db.execute(
                "SELECT count(*) FROM session_message WHERE session_id=? AND type='fixture_unknown_record'",
                (target,),
            ).fetchone()[0]
        check(unknown == 1, "Unknown message type not materialized")
        with connect(variants / "body-sentinel.db") as db:
            sentinel = messages(db, target)[execution["user_seq"]]["data"]["text"]
        check(sentinel == "FIXTURE_BODY_MUST_NOT_LEAK", "Body sentinel not materialized")

        lookup = first / "lookup"
        check((lookup / "explicit.db").read_bytes() == snapshot_path(oracle, "r1").read_bytes(), "Explicit lookup copy")
        check((lookup / "configured.db").read_bytes() == snapshot_path(oracle, "override").read_bytes(), "Configured lookup copy")
        check((lookup / "default.db").read_bytes() == snapshot_path(oracle, "default").read_bytes(), "Default lookup copy")
        check((lookup / "no-target.db").read_bytes() == snapshot_path(oracle, "override").read_bytes(), "Wrong-target lookup copy")
        check((lookup / "invalid.db").read_bytes() == b"not a SQLite database\n", "Invalid lookup control")
        check(not (lookup / "missing.db").exists(), "Missing lookup control exists")
        with connect(lookup / "empty.db") as db:
            check(db.execute("PRAGMA integrity_check").fetchone() == ("ok",), "Empty lookup integrity")
            check(all(db.execute(f"SELECT count(*) FROM {table}").fetchone() == (0,) for table in TABLE_SQL_SHA256), "Empty lookup has rows")
        requested = session_id(oracle, "r1", "T")
        with connect(lookup / "no-target.db") as db:
            check(db.execute("SELECT 1 FROM session_v2 WHERE id=?", (requested,)).fetchone() is None, "Wrong-target lookup contains target")
            check(db.execute("SELECT count(*) FROM session_v2").fetchone()[0] > 0, "Wrong-target lookup is empty")

    check(
        originals == {
            str(path.relative_to(RECORDS)): digest(path)
            for path in RECORDS.rglob("*")
            if path.is_file()
        },
        "Derived preparation changed source records",
    )


def verify():
    inventory, oracle = verify_bundle()
    verify_derived(oracle)
    print(
        f"Verified {len(inventory['files'])} native files, "
        f"{len(oracle['executions'])} executions, independent counters, references, clocks, API evidence, and privacy."
    )


if __name__ == "__main__":
    verify()
