#!/usr/bin/env bash
# Invoke through Devbox. Each mode is suitable for an independent CI job.
# Schema isolation does not isolate PostgreSQL's database-wide advisory locks.
# Serialize packages sharing the test database; concurrency within each test,
# including held-lock/multi-worker regressions, is unchanged.
set -euo pipefail
case "${1:-behavior}" in
  behavior) go test -p 1 -count=1 ./... ;;
  race) go test -p 1 -race -count=1 ./... ;;
  vet) go vet ./... ;;
  *) echo 'usage: scripts/check.sh [behavior|race|vet]' >&2; exit 2 ;;
esac
