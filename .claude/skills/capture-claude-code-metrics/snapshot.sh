#!/bin/sh
# Read-only snapshot of one Claude Code session's main and subagent transcripts.
# Usage: snapshot.sh PROJECT_DIR SESSION_ID NEW_DEST [COUNT]
#   PROJECT_DIR  e.g. ~/.claude/projects/<cwd-encoded>
#   NEW_DEST     private directory outside the repository (must not exist)
#   COUNT        optional: take COUNT snapshots one second apart (DEST/000 ...)
# Only complete JSONL lines are copied; a partially appended tail is dropped.
set -eu
project=$1 session=$2 dest=$3 count=${4:-}
test ! -e "$dest" || { echo "destination exists: $dest" >&2; exit 1; }

copy_lines() {
  python3 -I -c '
import sys
data = open(sys.argv[1], "rb").read()
open(sys.argv[2], "wb").write(data[:data.rfind(b"\n") + 1])
' "$1" "$2"
}

snap() {
  out="$1/$(basename "$project")"
  mkdir -p "$out/$session/subagents"
  copy_lines "$project/$session.jsonl" "$out/$session.jsonl"
  for f in "$project/$session/subagents/"*; do
    [ -e "$f" ] || continue
    case "$f" in
      *.jsonl) copy_lines "$f" "$out/$session/subagents/$(basename "$f")" ;;
      *) cp -p "$f" "$out/$session/subagents/" ;;
    esac
  done
}

if [ -z "$count" ]; then
  snap "$dest/projects"
else
  i=0
  while [ "$i" -lt "$count" ]; do
    snap "$dest/$(printf '%03d' "$i")/projects"
    i=$((i + 1))
    sleep 1
  done
fi
