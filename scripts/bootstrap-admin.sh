#!/usr/bin/env bash
# Run through Devbox after explicitly starting Compose. The password is never a CLI argument.
set -euo pipefail
IFS= read -r -s -p 'New local TED administrator password (blank allowed with default settings): ' ted_password
printf '\n' >&2
printf '%s' "$ted_password" | docker compose exec -T app ted bootstrap "${1:-admin}"
unset ted_password
