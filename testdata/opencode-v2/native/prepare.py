#!/usr/bin/env python3
"""Materialize derived SQLite and lookup cases from sanitized records only."""

import hashlib
import json
import shutil
import sqlite3
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parent


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def session_id(oracle, execution):
    return oracle["snapshots"][execution["snapshot"]]["sessions"][execution["role"]]


def message_data(db, target, seq):
    row = db.execute(
        "SELECT data FROM session_message WHERE session_id=? AND seq=?", (target, seq)
    ).fetchone()
    if row is None:
        raise ValueError(f"Missing recipe message: {seq}")
    return json.loads(row[0])


def write_message_data(db, target, seq, data):
    db.execute(
        "UPDATE session_message SET data=? WHERE session_id=? AND seq=?",
        (json.dumps(data, separators=(",", ":")), target, seq),
    )


def mutate(path, recipe, oracle):
    execution = oracle["executions"][recipe["execution"]]
    target = session_id(oracle, execution)
    operation = recipe["operation"]
    with sqlite3.connect(path) as db:
        db.execute("PRAGMA foreign_keys=ON")
        if operation == "remove-session":
            db.execute("DELETE FROM session_v2 WHERE id=?", (target,))
        elif operation == "remove-start":
            db.execute(
                "DELETE FROM session_message WHERE session_id=? AND seq=?",
                (target, execution["user_seq"]),
            )
        elif operation == "remove-end":
            db.execute(
                "DELETE FROM session_message WHERE session_id=? AND seq=?",
                (target, execution["idle_seq"]),
            )
        elif operation == "remove-model":
            db.execute("UPDATE session_v2 SET model=NULL WHERE id=?", (target,))
            for seq in execution["assistant_seqs"]:
                data = message_data(db, target, seq)
                data.pop("model", None)
                write_message_data(db, target, seq, data)
        elif operation == "remove-usage":
            for seq in execution["assistant_seqs"]:
                data = message_data(db, target, seq)
                data.pop("tokens", None)
                write_message_data(db, target, seq, data)
        elif operation == "duplicate-response":
            seq = execution["usage_seqs"][0]
            row = db.execute(
                "SELECT session_id, type, time_created, time_updated, data "
                "FROM session_message WHERE session_id=? AND seq=?",
                (target, seq),
            ).fetchone()
            duplicate_seq = seq + 1
            if db.execute(
                "SELECT 1 FROM session_message WHERE session_id=? AND seq=?",
                (target, duplicate_seq),
            ).fetchone() is not None:
                raise ValueError("Duplicate-response recipe has no free sequence")
            db.execute(
                "INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) "
                "VALUES (?, ?, ?, ?, ?, ?, ?)",
                ("msg_fixture_duplicate_response", row[0], row[1], duplicate_seq, row[2], row[3], row[4]),
            )
        elif operation == "remove-prior-invocation":
            prior = oracle["executions"][oracle["cases"]["R3"]["baseline_execution"]]
            db.execute(
                "DELETE FROM session_message WHERE session_id=? AND seq BETWEEN ? AND ?",
                (target, prior["segment"][0], prior["segment"][1]),
            )
        elif operation == "reverse-end":
            reversed_end = execution["started_at"] - 1
            data = message_data(db, target, execution["idle_seq"])
            data["time"]["created"] = reversed_end
            write_message_data(db, target, execution["idle_seq"], data)
            db.execute(
                "UPDATE session_message SET time_created=?, time_updated=? WHERE session_id=? AND seq=?",
                (reversed_end, reversed_end, target, execution["idle_seq"]),
            )
            db.execute("UPDATE session_v2 SET time_idle=? WHERE id=?", (reversed_end, target))
        elif operation == "append-unknown":
            seq = db.execute(
                "SELECT max(seq) FROM session_message WHERE session_id=?", (target,)
            ).fetchone()[0] + 1
            data = json.dumps({"time": {"created": execution["ended_at"]}}, separators=(",", ":"))
            db.execute(
                "INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) "
                "VALUES (?, ?, ?, ?, ?, ?, ?)",
                (
                    "msg_fixture_unknown_record",
                    target,
                    "fixture_unknown_record",
                    seq,
                    execution["ended_at"],
                    execution["ended_at"],
                    data,
                ),
            )
        elif operation == "body-sentinel":
            data = message_data(db, target, execution["user_seq"])
            data["text"] = "FIXTURE_BODY_MUST_NOT_LEAK"
            write_message_data(db, target, execution["user_seq"], data)
        else:
            raise ValueError(f"Unknown recipe operation: {operation}")
        db.commit()
        if db.execute("PRAGMA foreign_key_check").fetchall():
            raise ValueError(f"Recipe broke foreign keys: {recipe['name']}")
        db.execute("VACUUM")


def prepare(destination):
    if destination.exists():
        raise ValueError("Use a new output directory; existing data is never replaced")
    oracle = json.loads((ROOT / "expected.json").read_text())
    recipes = json.loads((ROOT / "variants.json").read_text())
    sources = {
        name: ROOT / "records" / snapshot["file"]
        for name, snapshot in oracle["snapshots"].items()
    }
    before = {name: digest(path) for name, path in sources.items()}
    variants = destination / "variants"
    variants.mkdir(parents=True)
    for recipe in recipes["variants"]:
        source = sources[recipe["base"]]
        target = variants / f"{recipe['name']}.db"
        shutil.copy2(source, target)
        mutate(target, recipe, oracle)

    lookup = destination / "lookup"
    lookup.mkdir()
    for name, base in {
        "explicit.db": "r1",
        "configured.db": "override",
        "default.db": "default",
        "no-target.db": "override",
    }.items():
        shutil.copy2(sources[base], lookup / name)
    empty = lookup / "empty.db"
    shutil.copy2(sources["r1"], empty)
    with sqlite3.connect(empty) as db:
        db.execute("PRAGMA foreign_keys=ON")
        db.execute("DELETE FROM session_message")
        db.execute("DELETE FROM session_v2")
        db.execute("DELETE FROM project")
        db.commit()
        db.execute("VACUUM")
    (lookup / "invalid.db").write_bytes(b"not a SQLite database\n")
    # lookup/missing.db is deliberately absent.

    after = {name: digest(path) for name, path in sources.items()}
    if before != after:
        raise ValueError("Sanitized source records changed during preparation")
    hashes = {
        str(path.relative_to(destination)): digest(path)
        for path in sorted(destination.rglob("*"))
        if path.is_file()
    }
    (destination / "hashes.json").write_text(json.dumps(hashes, indent=2) + "\n")
    return hashes


if __name__ == "__main__":
    prepared = prepare(Path(sys.argv[1]))
    print(f"Prepared {len(prepared)} derived/lookup files; no original records changed.")
