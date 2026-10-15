#!/usr/bin/env bash
set -euo pipefail
export TED_TEST_COMPOSE_PROJECT="ted-smoke-$$"
export TED_DB_PASSWORD=disposable-smoke-database
export TED_INSECURE_HTTP=true TED_BIND_ADDRESS=127.0.0.1 TED_PORT=0
export TED_TEST_EVIDENCE=".devbox/test-evidence/$(date -u +%Y%m%dT%H%M%SZ)-compose-$$"
export TED_CONFIG_DIR="$PWD/$TED_TEST_EVIDENCE/Config"
export TED_MEDIA_DIR="$PWD/$TED_TEST_EVIDENCE/Media"
export TED_MIN_PASSWORD_LENGTH=0
mkdir -p "$TED_TEST_EVIDENCE" "$TED_CONFIG_DIR/postgres" "$TED_MEDIA_DIR"
compose=(docker compose --env-file /dev/null -f compose.yaml -p "$TED_TEST_COMPOSE_PROJECT")
cleanup(){
 "${compose[@]}" logs --no-color > "$TED_TEST_EVIDENCE/compose.log" 2>&1 || true
 "${compose[@]}" down > "$TED_TEST_EVIDENCE/cleanup.log" 2>&1
}
trap cleanup EXIT
if ! "${compose[@]}" up -d --build --wait --wait-timeout 120 > "$TED_TEST_EVIDENCE/build.log" 2>&1; then
 echo "Compose startup failed; see $TED_TEST_EVIDENCE/build.log" >&2; exit 1
fi
node scripts/compose-smoke.mjs 2>&1 | tee "$TED_TEST_EVIDENCE/checks.log"
echo "Evidence: $TED_TEST_EVIDENCE"
