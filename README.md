# The Entertainment District

TED is a self-hosted discovery, source mapping, download and viewing-history server. **Teddy** is the native Flutter client. Go serves the React management UI/API; PostgreSQL stores durable state and River jobs. Cinemeta, TMDb/TVDB, Real-Debrid and Stremio supply the selected integrations. An external media server is not part of the current application.

Start with [PROJECT.md](PROJECT.md) for the product contract and [IMPLEMENTATION.md](IMPLEMENTATION.md) for the current engineering handoff, tests and gaps. [Teddy's README](../teddy/README.md) covers builds/client workflows. [Operations](docs/operations.md) covers configuration and recovery. [The home-server record](docs/deployments/home-server.md) identifies the owner's actual installation; do not start a second copy of its synchronization workers.

The local projects are sibling folders: `Projects/ted` contains the server, web UI and shared documentation; `Projects/teddy` contains the native app and its build scripts. Run Devbox from the folder whose commands you are using.

## Development tools

Use the pinned Devbox environment; do not rely on global Go, Node, Flutter or CocoaPods. Devbox does not automatically start Docker or PostgreSQL.

```sh
devbox install
devbox run tools-check
```

Tools/versions are in devbox.json and devbox.lock; web and Dart dependencies use their own lockfiles. Docker/Compose must be running on the host where container commands execute. On the owner's Mac, Colima is deliberately stopped and the live deployment is on the home server. Do not run the optional `devbox run docker-start` merely to inspect the project. A new developer may explicitly start their own Docker runtime; `docker-start`/`docker-stop` are Mac Colima helpers, not deployment prerequisites for Linux.

## New installation

These instructions create a **new** localhost development deployment. For an existing installation retain its `.env`, host files and accounts; never repeat bootstrap or point it at an empty Config directory. For the owner's deployment use its specific Compose project/overrides in the deployment record.

From the repository root, generate a new private configuration (the command refuses to overwrite an existing file):

```sh
devbox run -- node -e 'const fs=require("node:fs"); const password=require("node:crypto").randomBytes(32).toString("hex"); fs.writeFileSync(".env", `TED_DB_PASSWORD=${password}\nTED_BIND_ADDRESS=127.0.0.1\nTED_PORT=8080\nTED_INSECURE_HTTP=true\n`, {flag:"wx",mode:0o600});'
mkdir -p Config/postgres Config/metadata Media
id -u
id -g
```

Set `TED_UID` and `TED_GID` in `.env` to the owner of Media and Config/metadata using the printed IDs. Set `TED_TIMEZONE` to the intended IANA zone for release eligibility. See [.env.example](.env.example) and the configuration table in Operations. HTTP is deliberate for this localhost example; use an HTTPS reverse proxy and `TED_INSECURE_HTTP=false` for secure browser transport. Compose defaults to loopback, not public exposure.

```sh
devbox run -- docker compose up -d --build --wait
devbox run -- curl --fail http://127.0.0.1:8080/health/ready
```

Readiness returns `{"status":"ready"}`. Open the web UI to create the first administrator. Setup is available only when no accounts exist; concurrent submissions cannot create a second initial account. Complete setup before exposing a fresh installation publicly. Blank passwords are allowed when the configured minimum is zero. After creation, sign in normally. There is no default password, ongoing public registration or external identity-provider login. The optional CLI alternative remains `devbox run -- bash scripts/bootstrap-admin.sh admin`; it also refuses when any user exists.

Open [TED on localhost](http://127.0.0.1:8080/). The database is PostgreSQL 18 in explicit host files at Config/postgres; metadata is Config/metadata and owned server media is Media. Keep these outside disposable container layers and preserve them through upgrades. Docker is not a backup.

## Account and provider setup

In **Your account**, edit the display name separately from the unique login username. Administrators use **People** to create/disable accounts. Password changes and account disablement revoke all sessions; ordinary browser **Sign out** affects only that browser. **Sign out all devices** is the explicit account-wide action.

Use the pencil at the bottom left of your account icon to upload a profile picture (JPEG, PNG, WebP or GIF, at most 20 MiB and 40 megapixels). TED takes the first frame and crops the center to a square. Pictures are stored in PostgreSQL and included in database backups; no extra directory or provider configuration is needed. An updated Teddy fetches the picture when that profile is selected, on Reload, and within about a minute while it remains active. The chooser uses the saved local picture before signing in, including offline. Select each saved profile once to populate its picture; failed/offline refreshes retain it.

Administrators configure RD, TMDb API Read Access Token, TVDB API key and optional subscriber PIN under **Server providers**. Saved values are never returned. Blank inputs retain them; red Remove saved key buttons remove immediately. Configured status means saved, not verified. Database backups therefore contain sensitive provider state and must remain private.

**Personal Stremio watch state** connects only that user's two-way watched-history account. **Stremio add-ons**, below it for administrators, configures the single server-wide discovery account and its enabled variants. These connections are independent even when using the same external account. Reload add-ons retrieves the inventory; Torrentio and Torrentio RD remain separate public variants. Disabling prevents new lookup/selection/renewal without deleting shared preferences.

**Joint account settings** lets an account add users, or lets a member inspect/leave joined accounts and choose indicator colors. Members only read joint watch projections; no viewing merges or cross-account writes. Nested memberships are prohibited. Sharing an external Stremio history account is allowed with an explicit indirect-sync warning.

## Browse, map and watch

Movies/Series discovery supports **Mapped** and **Watched** filters. Click Watched to reveal account toggles on a separate row: your account is white, and each joint account uses its assigned color. All are enabled initially; results include titles viewed by any selected account, including joint-only titles. Deselect an account to exclude its unique results; deselect all for an empty list. Search, genre, Mapped and pagination apply to this combined list. These controls are read-only and never change anyone's history. Detail watched controls work for unmapped media too. Completed scopes show checkmarks; partial show totals display expanding watched/total pills at the top right, white for the active account and colored for joint accounts. Current completion and retained viewing are distinct.

Administrator RD mapping starts from a selected account torrent/file, not guessed title matching. Movie details map one file; show details support individual episodes and guided season ranges. Review choices before submission. Validation activates sequential atomic batches (200 assignments default; four workers). Failed batches retain previous mappings and completed batches; retry only unfinished work. Explicit remapping can interrupt active playback and changes the cut/offset if the source differs. The RD browser reads TED's inventory cache; refresh triggers incremental reconciliation, with full rebuild available under provider settings.

When playback needs to add a torrent to RD, TED selects only the requested episode/movie file. It reuses valid existing file links and preserves account selections; it no longer selects the entire pack before trying an individual episode. This is a server change and requires no Teddy update.

Device downloads in Teddy are separate from web/server-managed local copies. Web local-download actions operate on mapped server media and persistent transfer/validation/publication jobs. Never delete a file to fix a job manually; use the owning removal/retry workflow. Shared destructive actions require administrators. Removing an owned copy preserves personal history; whole-title removal retains tombstones and stable identity.

Watch edits offer dated or undated watched state where supported. Ordinary Unwatched retains history; confirmed Never watched clears only that user's title-scoped viewing/resume/counts. Stremio receives flags, not native resume positions. Failed reads retain previous state and must not lock controls merely because an obsolete response field is absent.

Automatic playback below 15% of known duration no longer creates history, Continue Watching or resume progress. At 15% it qualifies; backward seeking afterwards preserves it. Completion remains 90%, and explicit watched/Next actions remain valid at any offset. Existing saved history is not retroactively erased. See the full unknown-duration, placeholder-video and replay rules in PROJECT.

## Metadata and artwork

Cinemeta supplies initial titles/art/IMDb scores. TMDb enriches movies; TVDB enriches shows/seasons/episodes. Metadata is lazy and shared, watch classification personal. A TV highlight waits 250 ms before a missing metadata fetch; cached metadata is immediate. Failure retains usable cached content and supports retry. Schedules refresh independently each day; monthly metadata expiry is not implemented.

On administrator movie/show details, **Edit artwork** is beside RD Mappings. If uncached, TED builds metadata before presenting the editor. It exposes one Primary, Logo, Banner and Backdrop plus season posters; clicking a slot offers provider alternatives on demand or file upload. Overrides survive metadata refresh and failed replacements preserve the old image. Cast/person pages and character-art fallback are implemented in Teddy; general descriptive editing remains deferred.

## Verify and build

Web commands:

```sh
devbox run -- npm --prefix web ci
devbox run -- npm --prefix web test
devbox run -- npm --prefix web run build
```

Go behavior/race checks require FFmpeg and ffprobe on PATH, including the account-picture tests. Devbox supplies both; hosted Go test jobs explicitly install the FFmpeg package. `scripts/check.sh` fails before Go starts when a media tool is missing; vet does not require media tools. Go tests requiring persistence need a **disposable** PostgreSQL database. Never point `TED_TEST_DATABASE_URL` at production. The helper below starts only its own tmpfs PostgreSQL 18 container and removes it afterward; it requires an explicitly available Docker runtime. Do not run it by starting the owner's retired Mac deployment.

```sh
devbox run test-integration
devbox run test-integration-race
devbox run vet
```

Behavior and race are separate checks. `-p 1` serializes packages sharing database-wide advisory locks; concurrency within a test remains exercised. Without a database, plain Go test can skip persistence coverage, so it is not equivalent. `scripts/compose-smoke.sh` exercises an isolated built stack/account flow. Live RD/restore harnesses can create real provider effects or copy secrets; read each script's prerequisites and target before using it. Older restore harnesses are not proof of the latest checkpoint's complete recovery behavior.

Native commands run from the sibling Teddy folder:

```sh
cd ../teddy
devbox run -- bash scripts/check-teddy.sh
devbox run -- bash scripts/build-teddy-macos.sh
```

The Mac build requires full Xcode and emits a unique immutable app path. Quit the prior instance and open the new app yourself. Never replace libraries inside a running bundle. Other platform validation remains outstanding; see the native README and IMPLEMENTATION for exact local and hosted evidence. A local passing check does not establish a successful hosted build or publication.

## Troubleshooting and operational boundaries

- **Cast loads but person page fails:** the October 15 server fix restores the optional filmography pagination cursor. Reopen the person or use Retry after updating TED; no Teddy rebuild or metadata-cache reset is needed.

- **Incomplete watched state after integration removal:** the web validator previously required an obsolete sequence field. The October 15 update removes that requirement, retaining revision/version/flag validation. Reload the deployed web assets; do not reset watch state as a workaround.
- **Blank/loading Home after restart:** the new Teddy build uses scoped persistent snapshots for all five rows and refreshes them independently. A first use with no cache still requires a successful fetch. An auth failure retains offline downloads and reports sign-in needs.
- **Source lookup/stream latency:** retain the home's dual-stack Docker egress configuration. Previously repeated IPv4 stalls contributed to unreliable RD/add-on requests. A cached badge/HTTP 200 is not proof of playable content. Use the source picker Reload for fresh results; report title, episode, exact release and position when comparing startup.
- **Keychain errors:** use Request keychain access and permit the OS prompt. Other storage/persistence failures require their actual diagnostic; do not delete all profile data or repeatedly rotate refresh tokens.
- **Playback seek/resume:** transient EOF must not advance episodes. Report a reproducible source/position; preserve exact release and diagnostics. Old 18–22 second measurements included network/open work, not demonstrated decoder computation.
- **Server not ready:** inspect `docker compose ps` and `docker compose logs --tail=100 app` in the correct deployment. Failed migration stops startup rather than resetting state. Do not start duplicate sync workers or replace storage with empty folders.
- **Upgrade/restore:** take a coordinated backup first. Source, database, media and app artifacts must match; an image-only rollback across schema 41 retirement is unsafe. Follow [Operations](docs/operations.md).

Diagnostic evidence under ignored `.devbox/` may contain credentials, session state or database snapshots. Keep it private. Do not commit Config, Media, `.env`, build outputs or recovery archives. Required third-party provenance/notices remain in the repository; optional historical experiments are clearly marked and are not runtime requirements.

## Automated releases

Main/develop pushes build versioned GHCR images; PRs produce OCI artifacts. See [release policy and repository setup](docs/releases.md) for version allocation, compatibility, required permissions, and Teddy signing secrets.
