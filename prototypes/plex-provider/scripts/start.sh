#!/usr/bin/env bash
set -euo pipefail
prototype_dir="$(cd "$(dirname "$0")/.." && pwd -P)"
repo_dir="$(cd "$prototype_dir/../.." && pwd -P)"
cd "$repo_dir"
mkdir -p "$prototype_dir"/bin "$prototype_dir"/runtime/{media,provider,plex-config,transcode,go-cache}
if [[ ! -s "$prototype_dir/runtime/media/demo.mp4" ]]; then
  devbox run -- ffmpeg -hide_banner -loglevel error \
    -f lavfi -i testsrc2=size=640x360:rate=24 \
    -f lavfi -i sine=frequency=440:sample_rate=48000 -t 60 \
    -c:v libx264 -preset veryfast -pix_fmt yuv420p -c:a aac -b:a 96k \
    -movflags +faststart -y "$prototype_dir/runtime/media/demo.mp4"
fi
devbox run -- env GOCACHE="$prototype_dir/runtime/go-cache" GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -C "$prototype_dir" -o bin/provider-linux-arm64 .
devbox run -- docker compose -f "$prototype_dir/compose.yaml" up -d --build
printf 'Provider: http://127.0.0.1:42390\nPlex Web: http://127.0.0.1:32490/web\n'
