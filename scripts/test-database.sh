#!/usr/bin/env bash
# Invoke through Devbox. Creates and removes only its own disposable local container.
set -euo pipefail
mode="${1:-behavior}"
case "$mode" in behavior|race) ;; *) echo 'usage: scripts/test-database.sh [behavior|race]' >&2; exit 2;; esac
if [[ $# -gt 0 ]]; then shift; fi
container="ted-test-${mode}-$$"
log_dir=".devbox/test-evidence/$(date -u +%Y%m%dT%H%M%SZ)-${mode}-$$"
mkdir -p "$log_dir"
cleanup() {
  docker logs "$container" > "$log_dir/postgres.log" 2>&1 || true
  docker rm -f -v "$container" >/dev/null
}
trap cleanup EXIT
docker run -d --tmpfs /var/lib/postgresql --name "$container" -e POSTGRES_PASSWORD=ted-test-only -e POSTGRES_DB=ted_test -p 127.0.0.1::5432 --health-cmd='pg_isready -h 127.0.0.1 -U postgres -d ted_test' --health-interval=1s --health-timeout=3s --health-retries=30 postgres:18 > "$log_dir/container.txt"
ready=false
for ((i=0;i<40;i++)); do
  if [[ "$(docker inspect --format '{{.State.Health.Status}}' "$container")" == healthy ]]; then ready=true; break; fi
  sleep 1
done
if [[ "$ready" != true ]]; then echo "Database did not become healthy; see $log_dir" >&2; exit 1; fi
port="$(docker port "$container" 5432/tcp)"
export TED_TEST_DATABASE_URL="postgres://postgres:ted-test-only@${port}/ted_test?sslmode=disable"
export TED_REQUIRE_DATABASE_TESTS=1
if [[ $# -gt 0 ]]; then
  # Explicit Go arguments allow focused persistence checks; the default and CI
  # still run the entire suite. No shell templates or automatic retries.
  test_args=(-p 1 -count=1)
  if [[ "$mode" == race ]]; then test_args+=(-race); fi
  go test "${test_args[@]}" "$@" 2>&1 | tee "$log_dir/tests.log"
else
  bash scripts/check.sh "$mode" 2>&1 | tee "$log_dir/tests.log"
fi
echo "Evidence: $log_dir"
