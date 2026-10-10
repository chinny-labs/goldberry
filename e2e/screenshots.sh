#!/usr/bin/env bash
# Build and start the demo household, then retake docs/screenshots from it.
#   e2e/screenshots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
go build -o bin/goldberry-demo ./e2e/demo
bin/goldberry-demo -listen 127.0.0.1:18081 &
pid=$!
trap 'kill "$pid" 2>/dev/null || true' EXIT
for _ in $(seq 1 60); do
  if curl -fsS -o /dev/null http://127.0.0.1:18081/healthz 2>/dev/null; then break; fi
  sleep 1
done
(cd e2e && BASE_URL=http://127.0.0.1:18081 OUT=../docs/screenshots node screenshots.mjs)
