#!/usr/bin/env python3
"""Materialize derived fault/lookup cases from sanitized native records only."""

import hashlib
import json
import shutil
import sys
from datetime import datetime, timedelta
from pathlib import Path

ROOT = Path(__file__).resolve().parent


def read(path):
    return [json.loads(line) for line in path.read_text().splitlines()]


def write(path, rows):
    path.write_text("".join(json.dumps(r, separators=(",", ":")) + "\n" for r in rows))


def notifies(row, agent):
    text = row.get("content") or row.get("attachment", {}).get("prompt") or ""
    return f"<task-id>{agent}</task-id>" in text


def prepare(destination):
    if destination.exists():
        raise ValueError("Use a new output directory; existing data is never replaced")
    oracle = json.loads((ROOT / "expected.json").read_text())
    recipes = json.loads((ROOT / "variants.json").read_text())
    destination.mkdir(parents=True)
    for recipe in recipes["variants"]:
        target = destination / recipe["name"]
        shutil.copytree(ROOT / "records" / recipe["base"], target)
        execution = oracle["executions"][recipe["execution"]]
        file = target / Path(*Path(execution["file"]).parts[1:])
        parent = target / Path(*Path(oracle["files"][recipe["base"] + ":P"]["file"]).parts[1:])
        op = recipe["operation"]
        if op == "remove-file":
            file.unlink()
            continue
        if op == "truncate-tail":
            file.write_bytes(file.read_bytes()[:-8])
            continue
        rows = read(file)
        lo, hi = execution["segment_lines"]
        mine = lambda i: lo <= i <= hi
        if op in {"remove-start", "remove-end"}:
            del rows[execution["start_line" if op == "remove-start" else "end_line"] - 1]
        elif op == "remove-notification":
            agent = execution["agent_id"]
            write(parent, [r for r in read(parent) if not notifies(r, agent)])
        elif op == "remove-usage":
            for i, r in enumerate(rows, 1):
                if mine(i) and r["type"] == "assistant":
                    r["message"].pop("usage", None)
        elif op == "remove-model":
            for i, r in enumerate(rows, 1):
                if mine(i):
                    r.get("message", {}).pop("model", None)
                    r.pop("requestedModel", None)
                    r.get("params", {}).pop("model", None)
                    r.get("attachment", {}).get("identity", {}).pop("modelId", None)
            records = read(parent)
            for r in records:
                if r.get("toolUseResult", {}).get("agentId") == execution["agent_id"]:
                    r["toolUseResult"].pop("resolvedModel", None)
            write(parent, records)
        elif op == "duplicate-row":
            index = execution["usage_lines"][0] - 1
            rows.insert(index, rows[index])
        elif op == "remove-prior-invocation":
            prior = oracle["executions"]["T.A"]["segment_lines"]
            rows = [r for i, r in enumerate(rows, 1) if not prior[0] <= i <= prior[1]]
        elif op == "reverse-end":
            before = datetime.fromisoformat(execution["started_at"].replace("Z", "+00:00")) - timedelta(milliseconds=1)
            rows[execution["end_line"] - 1]["timestamp"] = before.isoformat(timespec="milliseconds").replace("+00:00", "Z")
        elif op == "append-unknown":
            rows.append({"type": "fixture_unknown_record", "timestamp": execution["ended_at"], "agentId": execution["agent_id"]})
        elif op == "append-sentinel":
            rows.append({"parentUuid": None, "isSidechain": True, "agentId": execution["agent_id"], "type": "user", "timestamp": execution["ended_at"], "message": {"role": "user", "content": "FIXTURE_BODY_MUST_NOT_LEAK"}})
        else:
            raise ValueError("Unknown recipe operation")
        write(file, rows)
    for name, base in {"explicit": "r1", "configured": "override", "default": "default", "no-target": "override"}.items():
        shutil.copytree(ROOT / "records" / base, destination / "lookup" / name)
    (destination / "lookup" / "empty").mkdir()
    # 'nonexistent' is deliberately not created.
    hashes = {str(p.relative_to(destination)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(destination.rglob("*")) if p.is_file()}
    (destination / "hashes.json").write_text(json.dumps(hashes, indent=2) + "\n")
    return hashes


if __name__ == "__main__":
    hashes = prepare(Path(sys.argv[1]))
    print(f"Prepared {len(hashes)} derived/lookup files; no original records changed.")
