#!/usr/bin/env python3
"""Materialize derived fault/lookup cases from sanitized native records only."""

import hashlib
import json
import shutil
import sys
from datetime import datetime, timedelta
from pathlib import Path

ROOT = Path(__file__).resolve().parent


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
        op = recipe["operation"]
        if op == "remove-file":
            file.unlink()
            continue
        if op == "truncate-tail":
            file.write_bytes(file.read_bytes()[:-8])
            continue
        rows = [json.loads(line) for line in file.read_text().splitlines()]
        if op in {"remove-start", "remove-end"}:
            index = execution["start_line" if op == "remove-start" else "end_line"] - 1
            del rows[index]
        elif op in {"remove-usage", "remove-prior-usage"}:
            rows = [r for i, r in enumerate(rows, 1) if not ((r["type"] == "token_usage_record" or r["payload"].get("type") == "token_count") and (op == "remove-usage" or i < execution["start_line"]))]
        elif op == "duplicate-response":
            index = execution["usage_lines"][0] - 1
            rows.insert(index, rows[index])
        elif op == "reverse-end":
            before = datetime.fromisoformat(execution["started_at"].replace("Z", "+00:00")) - timedelta(milliseconds=1)
            rows[execution["end_line"] - 1]["timestamp"] = before.isoformat(timespec="milliseconds").replace("+00:00", "Z")
        elif op == "append-unknown":
            rows.append({"timestamp": execution["ended_at"], "type": "fixture_unknown_record", "payload": {}})
        elif op == "append-sentinel":
            rows.append({"timestamp": execution["ended_at"], "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "FIXTURE_BODY_MUST_NOT_LEAK"}]}})
        elif op == "remove-model":
            for r in rows:
                if r["type"] == "turn_context":
                    r["payload"].pop("model", None)
            for parent in target.rglob("*.jsonl"):
                if parent == file:
                    continue
                records = [json.loads(line) for line in parent.read_text().splitlines()]
                for r in records:
                    item = r["payload"].get("item", {})
                    if item.get("agent_thread_id") == execution["thread_id"]:
                        item.pop("model", None)
                parent.write_text("".join(json.dumps(r, separators=(",", ":")) + "\n" for r in records))
        else:
            raise ValueError("Unknown recipe operation")
        file.write_text("".join(json.dumps(r, separators=(",", ":")) + "\n" for r in rows))
    for name, base in {"explicit": "r1", "configured": "override", "default": "default", "no-target": "override"}.items():
        shutil.copytree(ROOT / "records" / base, destination / "lookup" / name)
    (destination / "lookup" / "empty").mkdir()
    # 'nonexistent' is deliberately not created.
    hashes = {str(p.relative_to(destination)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(destination.rglob("*.jsonl"))}
    (destination / "hashes.json").write_text(json.dumps(hashes, indent=2) + "\n")
    return hashes


if __name__ == "__main__":
    hashes = prepare(Path(sys.argv[1]))
    print(f"Prepared {len(hashes)} derived/lookup files; no original records changed.")
