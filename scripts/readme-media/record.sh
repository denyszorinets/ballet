#!/usr/bin/env bash
# Re-records the README's GIFs and stills into docs/_static/readme: runs a
# throwaway Ballet with the fake model, drives it (record.mjs) and builds
# the GIFs (gif.py). Needs what `make run` needs, plus Chrome and uv.
set -euo pipefail
cd "$(dirname "$0")/../.."
tmp=$(mktemp -d)
trap 'kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null; rm -rf "$tmp"' EXIT
BALLET_RUN_DIR="$tmp/run" BALLET_FAKE_LLM=1 scripts/run.sh >"$tmp/run.log" 2>&1 &
pid=$!
for _ in $(seq 120); do
	grep -q "connected to Core" "$tmp/run.log" && break
	kill -0 "$pid" 2>/dev/null || { cat "$tmp/run.log"; exit 1; }
	sleep 1
done
node scripts/readme-media/record.mjs "$tmp/frames"
uv run --quiet scripts/readme-media/gif.py "$tmp/frames" docs/_static/readme
