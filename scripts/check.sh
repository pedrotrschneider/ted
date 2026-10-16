#!/usr/bin/env bash
# Invoke through Devbox. Each mode is suitable for an independent CI job.
# Schema isolation does not isolate PostgreSQL's database-wide advisory locks.
# Serialize packages sharing the test database; concurrency within each test,
# including held-lock/multi-worker regressions, is unchanged.
set -euo pipefail
mode="${1:-behavior}"
if [[ "$mode" == behavior || "$mode" == race ]]; then
  for tool in ffmpeg ffprobe; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      printf 'Missing required media tool: %s. Run through Devbox or install FFmpeg before testing.\n' "$tool" >&2
      exit 1
    fi
  done
fi
case "$mode" in
  behavior) go test -p 1 -count=1 ./... ;;
  race) go test -p 1 -race -count=1 ./... ;;
  vet) go vet ./... ;;
  *) echo 'usage: scripts/check.sh [behavior|race|vet]' >&2; exit 2 ;;
esac
