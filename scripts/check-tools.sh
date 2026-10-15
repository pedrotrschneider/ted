#!/usr/bin/env bash
set -euo pipefail

go version
gopls version
golangci-lint version
dlv version
air -v
node --version
npm --version
psql --version
pg_dump --version
ffmpeg -version
ffprobe -version
docker --version
docker compose version
docker buildx version
git --version
curl --version
jq --version
flutter --version

if [[ "$(uname -s)" == "Darwin" ]]; then
  colima version
  pod --version
  rsync --version
fi
