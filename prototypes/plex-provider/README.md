# Plex media-provider prototype

**Historical, unselected experiment.** TED now uses its own native Teddy client. This prototype is not deployed, not a runtime dependency and not a proposed next implementation stage. Commands/results below describe the isolated October10 experiment only; do not start its old services as part of TED operation. Preserve any owner runtime data and required notices. Current direction: [PROJECT](../../PROJECT.md).

This isolated experiment exposes a live Cinemeta catalog through Plex's media-provider API. It tests whether a remote catalog can be browsed and played without creating ordinary Plex library entries. It does not connect to TED's database, Real-Debrid, or production credentials.

**Result at the experiment checkpoint:** the server-side provider and native transcoder work, but the requested stock Plex UI experience is not established. Plex Web can show the generated title through a direct link; Play fails and the two catalog directories do not appear in normal discovery. See [RESULTS.md](RESULTS.md) for the observed boundaries and evidence.

## Contents

- `main.go`: standalone Go standard-library provider, with XML and JSON responses.
- `index.html`: diagnostic catalog/search page and generated video player. This page is not Plex client compatibility evidence.
- `compose.yaml`: separate Plex and provider containers, network, loopback ports and host bind mounts.
- `scripts/`: build/start, registration/API probe, HLS probe, restart check and evidence sanitization.
- `evidence/`: redacted measurements and source hashes. Older raw captures are explained in the results document.
- `runtime/`, `bin/`, `research/`: ignored local state, executables and downloaded official API documentation.

The catalog exposes Movies and Shows, up to 50 entries per category, basic show/season/episode navigation, and live Cinemeta search. The default list includes one generated fixture and up to 49 Cinemeta titles. Cinemeta entries have stable IMDb-based identities but **no mapped playable source**. The only playable source is an original 60-second test pattern and tone, used as a movie and an episode fixture.

## Run on this computer

Prerequisites: this repository's existing Devbox environment, Docker Compose, and the existing Colima Docker environment running. No Devbox dependency changes were needed. The Plex image and build target are pinned for this computer's Linux ARM64 environment; this setup has not been checked on another architecture.

From the repository root:

```sh
bash prototypes/plex-provider/scripts/start.sh
```

This generates the test clip, builds the provider and starts only the `ted-plex-provider-prototype` Compose project. It requires the two ports and `172.30.89.0/24` subnet to be free.

Open [Plex Web](http://127.0.0.1:32490/web), sign in and finish setup for the prototype server. Skip media folders. Setup is already complete on this computer; it does not need to be repeated. Then register and check the provider:

```sh
devbox run -- node prototypes/plex-provider/scripts/probe.mjs
```

The script reads only the owner token in this prototype's Plex configuration and sends it in an HTTP header. It does not print the token. It reuses the saved provider ID; repeated registration of the same provider URL produced a Plex database uniqueness error. Do not delete the connection file to force re-registration.

Open the [diagnostic page](http://127.0.0.1:42390). The Movies/Shows selector and Batman search should return Cinemeta metadata. The generated clip should play directly from the provider. “Test title inside Plex Web” opens the native fixture detail page. In the owner check, the title appeared but Play failed. The diagnostic page working does not resolve this failure.

To test Plex's native HLS transcoder:

```sh
devbox run -- node prototypes/plex-provider/scripts/transcode.mjs
devbox run -- ffmpeg -hide_banner -loglevel error -i prototypes/plex-provider/runtime/hls-check/segment-0.ts -t 5 -f null -
```

Expected: master and variant playlists plus three segments return HTTP 200, and FFmpeg exits successfully. The script stops its own transcoder session afterward. It records results even if the HTTP request fails; inspect `evidence/transcode.json` rather than interpreting script completion alone as success.

The API probe has the same rule: inspect `evidence/summary.json`. At present the range request fails HTTP parsing, native queues are empty and discovery excludes the provider, despite other requests returning 200.

## Local checks and restart verification

```sh
devbox run -- env GOCACHE="$PWD/prototypes/plex-provider/runtime/go-cache" go test -C prototypes/plex-provider ./...
devbox run -- env GOCACHE="$PWD/prototypes/plex-provider/runtime/go-cache" go test -C prototypes/plex-provider -race ./...
devbox run -- node prototypes/plex-provider/scripts/restart-check.mjs
devbox run -- node prototypes/plex-provider/scripts/sanitize-evidence.mjs
```

The restart check restarts only the two prototype containers, reads state before/after without generating a new watch event, and reads the prototype Plex database counts in read-only mode. It fails if identity or fixture state changes. The provider watch-state endpoint uses one synthetic profile, not real multi-user reconciliation. The API probe writes a synthetic paused event at 21 seconds; this is not evidence that Plex Web delivers legitimate playback history.

## Data and cleanup

All prototype state is in host files under this folder:

| Host directory | Container use |
| --- | --- |
| `runtime/plex-config` | Plex `/config`, including its database and owner token |
| `runtime/transcode` | Plex `/transcode` |
| `runtime/provider` | Provider `/data`, fixture watch state and token-free connection metadata |
| `runtime/media` | Provider read-only `/media`, generated test clip |
| `runtime/hls-check` | Downloaded test segments for decode verification |

The ports bind to loopback. The isolated container subnet is allowed without authentication for this local experiment. The provider itself does not enforce production authentication. This is development configuration, not a deployment configuration for TED.

Stop and remove only the prototype containers/network, preserving all host state:

```sh
devbox run -- docker compose -f prototypes/plex-provider/compose.yaml down
```

No existing TED files, databases, containers or Devbox manifests were changed by this experiment. To retire it completely, first run the command above, then deliberately remove this prototype folder. Its ignored Plex configuration contains the signed-in account session and must not be committed or shared. For a local backup of this experiment, stop its containers and copy the folder including `runtime/`; this is not a general Plex recovery procedure.

Protocol references: [Plex Media Server API](https://developer.plex.tv/pms/) (Media Providers, API specification 1.2.3) and [official Plex Docker image](https://github.com/plexinc/pms-docker). Provider code is original; no third-party application libraries were added.
