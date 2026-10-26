# Owner home-server deployment

Updated October 25, 2026. This records the installed home-server preview on port 33043, not broad platform acceptance. The separate CasaOS installation on port 33044 remains `ghcr.io/pedrotrschneider/ted:0.2.0` and was not changed by this preview rollout. The old Mac server is stopped; do not start its stale synchronization workers. General backup/restore is in [Operations](../operations.md); code/test status is in [IMPLEMENTATION](../../IMPLEMENTATION.md).

Earlier October 25 read-only probes used the former cache-flush/schema-46 preview's configured providers without metadata/history writes. The owner subsequently requested the new rollout below. Earlier probe evidence remains `.devbox/viewing-cast-20261025/live-cast-server.json`, `cast-followup.json` and `.devbox/series-providers-20261025/live-provider-results-second.jsonl`.

## Cache retirement and metadata-provider rollout — October 25

Preview is cache-free, with 5% viewing qualification, 95% completion, distinct Next Up/New Releases, scoped cast and generic admin per-series provider/structure choice. The old app was stopped before the backup and forward migrations **47/48**. The old cache prototype used an unreleased 0.3 number too; use the matching new Mac artifact below. Migration 46 remains in history; all permanent state/replay lineage is preserved. The disposable cache mount is removed, while its former Config/playback-cache folder is retained on disk. October 20's cache/seekbar instructions below are historical.

## Last verified installation — October 25

| Item | Value |
| --- | --- |
| SSH host | `ptrschneider@100.122.31.92` over Tailscale, Linux x86_64; LAN address `192.168.1.2` |
| Web/LAN | `http://192.168.1.2:33043` |
| Deployment root | `/home/ptrschneider/projects/ted-teddy-preview` |
| Compose | project `ted-teddy-preview`; `compose.yaml`, `compose.home.yaml`, private `.env` |
| Image | `ted-home:20261025-watch-history-actions` |
| Image ID | `sha256:ec11fa98f0fb74a5a6814d04b5dc012f2470c4f93525e4bd9aa61d3512810cb0` |
| Source | `releases/20261025-watch-history-actions` |
| Source archive SHA256 | `5aaf701e44c25afa3f6b5a62e39065afbb43eda6549bd4c9fec9b93a44260f21` |
| Reported version | `0.3.0-preview.20261025.watch-history-actions`, required compatibility version `0.3` |
| Database | PostgreSQL 18; TED schema 48; host Config/postgres |
| Files | Config/metadata and Media bind mounts; old Config/playback-cache and historical Config/recovery retained on disk without active mounts |
| Current Mac build | `/Users/ptrschneider/Projects/teddy/build/releases/20261025T222326Z-PINdxegM/Teddy.app` (0.3.0 universal arm64+x86_64, 201.7 MB); built/deep-signature-verified, not launched or installed |

Provider credentials are in the database/admin settings, not frontend files. `.env` holds deployment configuration/database password. Preserve the Compose override's `default` database network **and IPv6-enabled `egress` network**. Earlier IPv4-only container egress caused intermittent add-on/RD connection stalls even when host IPv6 requests succeeded; dual-stack egress was part of the accepted reliability correction. Other hosts must verify their route rather than copying this assumption blindly.

## Latest rollout verification — web history controls and favicon

The shared movie/series header keeps clear-history immediately after the watched-state button and before Watch List/Rate. Favicon delivery now admits exactly `/icon.png`; generated files use 0644 and the HTML query key bypasses cached 404s. The browser URL returns the exact 14,948-byte TED PNG with image/png MIME and is readable by runtime UID/GID 10001. Go webui behavior/races/vet, 125 web checks and build pass. Private images/source maps remain excluded. The prior clock report matched Native Tester's retained viewing with current completion false; no badge or viewing semantics were changed.

Fresh stopped-app backup: `backups/pre-20261025-watch-history-actions-verified/`; independent stopped restore: `restore-checks/20261025-watch-history-actions-verified/`. All **82** table fingerprints, **80** retained tables through the no-op migration, and **1,539** file hashes match. Live protected state, provider choices and manual overrides match; database/CasaOS identities and permanent mounts/networks are unchanged. Binary SHA256: `e295408e3deef7d32fa27a55f824d785609495525bec5adad7cb48e91024f8f1`. Evidence: `.devbox/watch-history-actions-20261025`.

Teddy's **1,527** inputs remain unchanged; use the existing current Mac app above. No new native build, GUI, history test writes, commit/push/tag. Browser visual acceptance remains manual; HTTP/favicon/version/auth/health checks pass.

## Earlier October 25 verification — series actions and character labels

The Mac options action is last after Rate, with regular outlined button styling and no visible text. TMDb character labels omit the trailing `(voice)` marker in fresh and retained cast/person credits. Actual read-only JoJo series and default scoped season-6/episode-6 cast checks returned all five new voice actors with clean labels. This does not establish physical TV acceptance.

Fresh backup: `backups/pre-20261025-series-actions-voice-verified/`; stopped independent restore bind: `restore-checks/20261025-series-actions-voice-verified/`. Network-disabled PostgreSQL 18 matched all **82** restored table fingerprints, the schema-48 no-op migration preserved 80 retained tables, and **1,522** file hashes match. Protected live state, all three provider choices and all 23 custom artwork overrides match. Database/CasaOS start identities and permanent mounts/networks are unchanged. Binary SHA256: `60b728ae4a92c916c75c03b435184b51f81ebd70605127a973c117473a09cfdd`.

Metadata behavior and relevant independent races, Go vet/Linux build, 27 native tests and clean analysis pass. Web sources are unchanged from the preceding checked build. The isolated universal Mac build is signature-verified and all **1,527** inputs remained unchanged. Evidence: `.devbox/series-actions-voice-20261025`; no owner-data test writes, GUI, commit/push/tag or new dependency. Earlier image/apps/backups remain.

## Earlier October 25 verification — lazy metadata and override consent

The follow-up switches only the current series/root and verified structural index, reuses exact cached episode links, and leaves child metadata/cast lazy. TED and Teddy ask whether to remove manual overrides (default) or keep them, including hidden seasons; stale consent is rejected. Existing owner overrides are not cleared by deployment.

Fresh stopped-app backup: `backups/pre-20261025-lazy-series-metadata-verified/`; stopped independent restore bind: `restore-checks/20261025-lazy-series-metadata-verified/`. Network-disabled PostgreSQL 18 restored all **82** table fingerprints and the no-op schema-48 migration preserved **80** retained tables; **1,497** metadata/media/recovery file hashes match. No copied application workers ran. Live accounts/viewing/history/preferences/mapping versions/local files/memberships/receipts and all three provider choices match before/after; all **23** manual artwork overrides retain their hashes/generations. Database and CasaOS identities/start times, permanent mounts and networks remain unchanged. Binary SHA256: `da2a662cfccd20f66806e3b927bf2ba88bccedf949fa9ba94ac919a6e1a294fd`.

Relevant metadata/HTTP/catalog behavior, independent metadata/provider/artwork HTTP races, Go vet/Linux build, 125 web tests/build, 27 native series/metadata client tests and clean native analysis pass. The isolated Mac build is deep/strict signature-verified, with all **1,527** inputs unchanged; it was not launched. Evidence: `.devbox/lazy-series-metadata-20261025`. The first restore attempt caught a missing executable bit on the copied migration-only helper and automatically restarted the old preview; its backup/logs remain. The corrected helper completed the independent restore before cutover. Schema and compatibility remain 48/0.3. Owner provider-switch/physical-TV acceptance remains manual; no history/preferences were edited as acceptance tests. No commit/push/tag.

## Initial October 25 rollout verification

Stopped-app backup: `backups/pre-20261025-series-providers/`. Its isolated PostgreSQL 18 restore matched all **84** public tables. A migration-only helper then applied 47/48 without starting app workers or enabling networking; all **77** retained tables matched, normalizing only the two new schedule columns. All **1,417** durable metadata/media/recovery file hashes match. The restore container was removed; stopped bind storage remains `restore-checks/20261025-series-providers/`. Backups/older images/apps remain intact; image-only downgrade across these forward migrations is not a rollback workflow.

Live before/after fingerprints preserve **5 users**, **21,335 current viewing rows**, **16,521 history rows**, **13 account preferences**, **827 native receipts**, two mapping versions, one local file and two memberships. Database and CasaOS image/container/start identities are unchanged. Binary hash, source archive/image, schema 48, permanent mounts, networks, current browser/native authentication and old/missing native-contract 426 responses pass. Health is healthy, zero restarts and zero warnings/errors in the bounded startup log. The new web bundle contains the provider menu. No owner provider choices/history were altered as acceptance tests; actual series switching/cast/UI acceptance remains manual.

The Mac `http://127.0.0.1:33043` forward is verified. Its existing Tailscale master incorrectly targeted `100.122.31.92:33043`, while Docker publishes only on the LAN listener. Only that forward was corrected to `192.168.1.2:33043`, preserving the local profile origin and SSH master. For a fresh away-from-home connection use:

```sh
ssh -MNf -o ControlMaster=auto -o 'ControlPath=~/.ssh/ted-%C' -o ControlPersist=12h -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -L 127.0.0.1:33043:192.168.1.2:33043 ptrschneider@100.122.31.92
```

The image uses the retained October 19 Debian/FFmpeg runtime with the new Linux amd64 Go binary/web bundle; network-disabled FFmpeg/ffprobe checks pass. Full Go behavior, relevant independent races, vet/build, 125 web tests/build and 707 native tests/clean analysis pass. All 1,527 native inputs stayed unchanged across the isolated Mac build. Evidence is `.devbox/series-providers-20261025` and its sibling native directory. The first preflight stopped before effects because the mapping view was listed as a table; the corrected run fingerprints the real `mapping_versions`, including inactive versions. No assertion was relaxed. No GUI, commit, push or release tag.

## Historical native seekbar update

October 20: the then-current Mac app was `/Users/ptrschneider/Projects/teddy/build/releases/20261020T161358Z-Sefl2mKI/Teddy.app` (0.3.0, universal arm64+x86_64, deep/strict signature verified). It adds diagonal hatching to seekbar regions not confirmed cached on TED, with solid cached spans, a clear thumb/chapter gaps and no texture while caching is disabled/unknown/offline. All 711 curated tests, analysis and eight focused tests pass; rendered desktop/TV-scale checks and the isolated 659-input source comparison are in `../teddy/.devbox/cache-hatching-20261020`. Quit the older app before opening this path. It has not been launched/installed by automation. This client-only visual change did not restart or redeploy the server; the image/version/schema below remain the last verified server rollout.

## Prior cache rollout (withdrawn)

October 20 startup-prefix cache correction: `ted-home:20261020-playback-cache-flush`, reported `0.3.0-preview.20261020.cache-flush`, compatibility 0.3 and schema 46. During the owner's playback, the server had about 195 MiB committed and a live lease but no timeline points. Background fill repeatedly failed with `unexpected EOF`; chunk zero was absent. The in-process adapter lacked HTTP flush support, so the startup optimization ended the response after its prefix instead of opening the remainder. Its pipe writer now implements the flush contract without buffering or changing range/authentication validation.

A regression reproduced a 4,096-byte return for a 16,000-byte request before the fix, then passed with the complete original bytes, including same-file link renewal. A second regression checks prefix flush plus reader-close cancellation. Full cache/add-on suites, focused adapter/cache races and vet pass; the isolated test PostgreSQL is stopped. No native changes or Mac rebuild are required.

The owner stopped playback and authorized interruption. Stopped-app backup `backups/pre-20261020-playback-cache-flush/` restored all **84 table fingerprints** and **1,417 durable-file hashes** in network-disabled PostgreSQL 18 with no app workers. Removed only its test container, retaining `restore-checks/20261020-playback-cache-flush/`. Live protected accounts/history/preferences/mappings/memberships match; prior cache bytes/settings, original binds/networks and CasaOS container/image/start identity remain. Exact binary/image/source (622 matching inputs), readiness, native/browser auth/compatibility, Mac forward and a newly completed cache maintenance job pass; zero restarts and no app warnings/errors. The first CLI readiness probe ran before the listener and failed, then subsequent probes passed; it was not a container crash.

Evidence is `.devbox/playback-cache-flush-20261020` and `.devbox/cache-range-debug-20261020`; the retained failing regression and live observations explain the correction. Image archive SHA256: `41160989b9abe3087d0a5d80b519793032f21a29ffbd889084fa03a518ec0364`. Reopen playback in the same signed 0.3 Mac app to verify the actual source's growing cache and seekbar shading; no playback/history was created by automated verification. No GUI, commit, push or tag.

### Previous 0.3 scheduler rollout

October 20 playback cache: preview runs `ted-home:20261020-playback-cache-scheduler`, reported `0.3.0-preview.20261020.cache-scheduler`, compatibility **0.3**, schema **46**. Cache is enabled with the owner's **200 GiB** budget and **10-minute** unused retention. The dedicated `Config/playback-cache` → `/playback-cache` bind has the app's 1000:1000 ownership and verified ownership marker/write permission. Original database/metadata/Media binds, default network and IPv6 egress are preserved. Admins can disable caching or change capacity/retention in Your account → Playback cache.

Before the 45→46 migration, stopped-app backup `backups/pre-20261020-playback-cache-r2/` and a network-disabled PostgreSQL 18 restore preserved all **79 existing public-table fingerprints** and **1,413 durable-file hashes**. The migration-only helper reached 46 with no application workers, preserving every retained table; new cache tables were empty and settings matched the approved defaults. A subsequent backup for the final scheduler correction is `backups/pre-20261020-playback-cache-scheduler/`; its isolated restore preserved all **84 schema-46 public tables** and the same **1,413 durable files**. Removed both test containers and retained stopped bind storage under the matching `restore-checks/` paths.

Final live protected content fingerprints match: **5 users**, **21,312 current rows**, **16,495 history rows**, **13 account preference rows**, two mappings, one local file, two joint memberships and no avatars. CasaOS remains `ghcr.io/pedrotrschneider/ted:0.2.0` with identical container/image/start identity. The existing Mac `http://127.0.0.1:33043` forward serves 0.3. Healthy with zero new-container restarts; exact binary/image/source identities, native/browser authentication, old-contract 426 responses and completed cache maintenance jobs are verified. No owner playback, history or preferences were created by checks.

The initial 0.3 deployment exposed a real scheduling defect during final verification: only River's elected client enqueues its periodic jobs, so a second cache client never scheduled cleanup. The final image registers cache queues/periodics and all runtime worker kinds with the common scheduler; metadata remains a worker-only client. The ten-minute fill deadline has a fifteen-minute rescue threshold. Two real-database regressions start metadata first and prove expired disk bytes are removed with caching enabled or disabled, without touching permanent media/history. Full affected streaming/cache/metadata suites, focused scheduler/cache/metadata races and vet pass. The isolated local PostgreSQL test service is stopped.

The first deployment attempt also stopped before migration on an overly specific Compose normalization assertion, then safely restarted the old preview. The check was corrected to match the observed bind representation and the successful attempt used a fresh backup. Docker 29 reports the loaded manifest digest as its image ID; re-export verified that the exact prepared configuration/layers were retained. These diagnostics and unsuccessful checks remain alongside successful evidence; none were skipped or relaxed without establishing the cause.

Final image archive: `/Users/ptrschneider/Projects/ted/.devbox/playback-cache-scheduler-20261020/ted-home-20261020-playback-cache-scheduler.tar.gz`, SHA256 `e84cd6a573060b1455b6f34250d0441f00b56758582a97ee2f316d996b272df9`. Source archive beside it contains 621 tracked/new files; all matched the image snapshot before deployment. Runtime FFmpeg/FFprobe executed in network-disabled smoke containers. Evidence: `.devbox/playback-cache-scheduler-20261020/deployment`; initial migration/restore and scheduling diagnostics: `.devbox/playback-cache-20261020/deployment`. Earlier images/apps/backups remain. Migration 46 is forward-only; an image-only downgrade across 0.2/0.3 is not a rollback workflow.

Mac acceptance uses `/Users/ptrschneider/Projects/teddy/build/releases/20261020T045754Z-4lwecepj/Teddy.app` (signed universal 0.3.0). Quit the older app before opening it; no GUI/app installation or launch was automated. The server-only scheduler correction needs no native rebuild. Owner tests remain: an already-ready source's pause/resume, server seekbar shading, cached/uncached seeking, eligible next-episode prefetch, admin disable and the configurable paused-player timeout. Real provider playback and Chromecast/Windows/Linux acceptance are not established by deployment/API checks. No commit, push or release tag was made.

### Previous indicator artwork rollout

October 19 avatar/rating artwork refinements: `ted-home:20261019-indicator-polish`, reported `0.2.0-preview.20261019.indicator-polish`, compatibility 0.2 and schema 45. Web/native Absolute cinema palms are uniformly scaled, shorter and farther apart. Other-account rating circles are 50% larger with an outward corner offset keeping initials visible; web avatar wrappers remain square. The accompanying `20261019T154115Z-pzCJ69Wu/Teddy.app` also fits sidebar pictures/initials into circular bounds and preserves their size during the rail transition. Server Go/schema code compares unchanged with the previous verified snapshot; no migration.

Stopped-app backup: `backups/pre-20261019-indicator-polish/`; all 79 public-table fingerprints and 1,399 durable-file hashes matched an isolated PostgreSQL 18 restore with network disabled and no app workers. Removed only the test container; retained storage is `restore-checks/20261019-indicator-polish/`. Live protected fingerprints match, including 11 account preference rows, five users, 21,311 current rows, 16,494 history rows, two mappings, one local file, two memberships and no avatars. Original bind mounts/default+IPv6 networks and CasaOS container/image/start identity are unchanged. Exact image/version/schema, readiness, zero new-container restarts, Mac localhost forward and native/browser authentication/compatibility boundaries pass. The first readiness probe preceded the listener; later checks succeed. Evidence: `.devbox/indicator-polish-20261019`.

Mac: isolated universal 0.2.0 app, signed/verified, with 512 app-owned source files matching its isolated copy. Final native analysis and 694 curated tests, 60 focused native tests, 124 web tests/build and three rendered browser checks pass. Quit the old Teddy and open the immutable path above; automation did not launch or replace a running app. Owner Mac/Chromecast visual acceptance remains pending. No owner viewing/preferences were changed by tests, GUI operated, commit/push or release tag made. Earlier images/apps/backups remain.

### Previous poster/ratings rollout

October 19 poster/ratings fixes: preview now runs `ted-home:20261019-poster-indicator-fixes` (`0.2.0-preview.20261019.poster-fixes`, required compatibility 0.2). TED's poster captions no longer add padding/display/color to nested badges; bookmarks, initials and small rating glyphs are centered. Person ratings remain anchored to avatars even beside progress. Direct joint badges carry only that joint's own rating when it has viewed the title. No migration; schema remains 45. The immutable Mac build above adds Ratings viewport/focus preservation and Appearance → Poster indicator size (75–300%, default 200%), covering all artwork indicators with adaptive account-column/+X fitting.

Consistent stopped-app backup: `backups/pre-20261019-poster-indicator-fixes/`, including the database, private deployment configuration, metadata, Media and retained recovery. Restored all **79 public tables** into a new bind-mounted PostgreSQL 18 container with `--network none` and no app workers; every table fingerprint and **1,385 durable-file hashes** matched. Removed only the test container, retaining stopped storage at `restore-checks/20261019-poster-indicator-fixes/`.

Live protected fingerprints match before/after: five users, 21,311 current rows, 16,494 history rows, two mappings, one local file, two memberships, no avatars and **ten account/title preference rows**. Verified the exact image/version, schema, readiness, zero new-container restarts, original Config/Media mounts and both default/IPv6 egress networks. The Mac localhost forward serves the new version; current native/browser collections require authentication and missing/0.1 native contracts return 426. CasaOS container/image/start identity remains unchanged. One first readiness probe again preceded the HTTP listener and logged a connection failure; subsequent health checks succeed. Evidence: `.devbox/poster-fixes-20261019`, including restored/live fingerprints, source/build/check manifests and `live-check.json`.

Mac: isolated 0.2.0 universal build and signature verification pass; all 512 app-owned source files matched its copy. Quit the previous Teddy and open the unique path above; it has not been launched or installed by automation. Owner Mac/Chromecast acceptance remains pending. No owner viewing/preferences were changed by checks, GUI operated, commits/pushes or release tags made. Prior builds/images/overrides/backups remain available.

### Previous Watch List/ratings rollout

October 19 Watch List/ratings rollout: owner-authorized preview update from the verified 0.2 working tree. The new contract deliberately requires `X-Teddy-Compatibility-Version: 0.2` and explicit title watch-list/rating fields; use the Mac build above. The matching frontend includes Watch List, seven ratings and account-only Unrated, shared poster icons and other-user rating projections. Schema 44→45 adds only the account/title preference table/indexes. Existing current/history/mappings, profile/token scopes and media remain intact. No owner viewing was created by verification.

Consistent stopped-app backup is `backups/pre-20261019-watchlist-ratings/`, with PostgreSQL custom dump/archive listing, private deployment configuration, Compose files, metadata, Media and retained recovery. All 1,319 durable-file hashes matched. Restored all 78 public tables into a new bind-mounted PostgreSQL 18 container with `--network none`; fingerprints matched before migration. Ran the migration-only helper to schema 45 without any app/worker process; all retained old-table fingerprints still matched and the new preference table was empty. Removed only the test container and retained its stopped cluster at `restore-checks/20261019-watchlist-ratings/`.

Live protected row/content fingerprints match before/after: five users, 21,311 current rows, 16,494 history rows, two mappings, one local file, two memberships and no avatars. Exact image/version/schema, healthy readiness, zero restarts, original bind mounts and both default/IPv6 egress networks are verified. The Mac's existing `http://127.0.0.1:33043` forward serves the same 0.2 contract. New native/browser collection routes reject unauthenticated reads; native missing/0.1 contracts return 426 before authentication. CasaOS remains on `ghcr.io/pedrotrschneider/ted:0.1.10` with identical container, image and start identity. Evidence: `.devbox/watchlist-ratings-20261019` (`deployment.json`, `restore.json`, `live-check.json`, source/build/check manifests and logs). The Mac build has not been launched and runtime owner acceptance remains pending. No commits/pushes/releases were made.

One initial readiness CLI invocation preceded `TED API starting` by roughly 35 ms, so it logged `TED stopped` for its failed connection. The live server continued starting normally; later probes succeed and no server restart occurred. Preserve both the initial log and successful checks.

Prior image/override and backups remain. Migration 45 is forward-only; an older image may not accept the newer migration history. Repair a failed new app in place or perform a reviewed restore with session/viewing reconciliation; do not automatically overwrite current state or downgrade only the image.

### Previous series/person-indicator rollout

October 17 series/person-indicator rollout: at the owner's request, built the verified working tree on the home server and replaced only the preview app. Includes the Series labels, optional read-only other-user watch projections/authenticated avatar reads and vertical adaptive badges with +X overflow. It also includes the subsequently approved exact-file RD policy: new playback preparation selects only the requested movie/episode, replacing the previous all-files-first fallback. Database schema remains 44; no migration or compatibility-major change.

Consistent stopped-app backup: `backups/pre-20261017-series-user-indicators/` contains a PostgreSQL custom dump and archive listing, Compose files/private deployment configuration, metadata, Media and retained recovery. Every one of the 1,316 copied durable files matched its original hash. Restored the dump into a fresh bind-mounted PostgreSQL 18 container with `--network none` and no app/worker process; all 78 public-table counts/content fingerprints matched. Removed that test container, retaining its stopped storage in `restore-checks/20261017-series-user-indicators/`.

Live before/after fingerprints match for users, current/history, mappings/files, memberships and avatars (5 users, 21,050 current rows, 16,233 history rows, 2 mappings, 1 local file, 2 memberships; no uploaded preview avatars). Verified the exact image/version, readiness, zero restarts, original bind mounts and both networks. The new native picture route requires authentication and the served frontend contains the new vertical indicator CSS. The Mac's existing `http://127.0.0.1:33043` forward returns the new version, preserving saved profile/download scope. CasaOS container/image/start time were unchanged. Prior image/override and backup remain available. Evidence: `.devbox/preview-indicators-20261017` (`deployment.json`, `restore.json`, `source.json`, build/deploy logs).

This rollout did not sign into accounts, change viewing, operate the Mac GUI, install/launch Teddy, commit/push or publish a release. Use the new immutable Mac app above with the existing preview profile; Reload refreshes its artwork/watch data. Person/photo/progress rendering and actual-TV acceptance remain owner checks.

### Previous playback/RD fallback rollout

October 17 playback/RD fallback rollout: deployed the tested working tree to the preview after the owner reported that the running service still produced the old error. The new resolver first retains all-file season selection, then uses a separate exact-episode RD entry if unrestrict confirms a single RAR archive. Native failure logging and schema 42–44 are included. Docker image built on the home server from the immutable source release above; no local Colima instance or CasaOS service was started/replaced.

Consistent stopped-app backup: `backups/pre-20261017-playback-rd-fallback-r2/` contains PostgreSQL custom-format dump, archive contents listing, `.env`, Compose files, metadata, Media and retained recovery files. Restored the dump into a separate bind-mounted PostgreSQL 18 container with `--network none`, ran only the migration helper to reach schema 44 and compared account/current/history/mapping/file row fingerprints. No restored application or synchronization worker started. The test container was stopped/removed; its stopped cluster remains privately under `restore-checks/20261017-playback-rd-fallback-r2/`.

Live pre/post upgrade fingerprints also matched, with 5 accounts, 21,048 current rows, 16,231 history rows, 2 mappings and 1 local file. Readiness HTTP 200, exact preview version and image ID verified; healthy with zero restarts. Original Config/Media mounts and default/IPv6 egress networks retained. CasaOS image/container startup identity unchanged. Evidence: `.devbox/preview-playback-20261017`. This proves the deployment/upgrade, not end-to-end playback in the owner's current Teddy session; retry the same source for acceptance.

The first isolated restore attempt failed because its new host bind root was mode 0700 owned by the SSH user, so PostgreSQL could not traverse it after dropping privileges. The old preview automatically restarted; its database was untouched. Retained the first backup and failure log, then corrected only the new restore bind's ownership to the container PostgreSQL user before the successful second rehearsal. No checks were skipped or relaxed. Previous image `ted-home:20261015-person-cursor` and both backups remain. Returning to schema 41 requires a matching restore plus reconciliation of newer state, not an unreviewed image-only downgrade.

### Previous person-page rollout

October 15 person-page fix: terminal filmography responses omit the optional cursor instead of returning an invalid empty token. API behavior and race regressions passed. Deployed after owner stopped playback; backup `backups/pre-person-cursor-20261015.dump`, override `backups/compose.home.pre-person-cursor.yaml`. Schema 41 and before/after counts preserved: 5 users, 20,623 current, 15,806 history, 2 mappings, 1 local file. Healthy, zero restarts, LAN HTTP 200. Previous image `ted-home:20261015-folder-split` retained. Existing Teddy build works with this server fix. Evidence: `.devbox/person-cursor-20261015`.

### Previous folder-split rollout

October 15 post-folder-split rebuild: local Go build, production web build and 112 web tests passed from `Projects/ted`; the new home-server image was built and deployed at the owner’s request. Backup `backups/pre-folder-split-20261015.dump` and override `backups/compose.home.pre-folder-split.yaml` precede replacement. Idle checks passed; schema 41 and counts were unchanged by deployment (5 users, 20,621 current, 15,803 history, 2 mappings, 1 local file). Container healthy with zero restarts; LAN web HTTP 200. Prior image `ted-home:20261015-joint-filter` remains available. Evidence: `.devbox/relocation-build-20261015` in the relocated TED folder.

### Previous Watched-filter rollout

October 15 Watched account-filter update adds an all-selected-by-default union of the active account and selected joint accounts in web Movies/Series. No migration or native app change. Idle checks, backup/archive verification and health checks passed. Backup: `backups/pre-joint-filter-20261015.dump`; prior override: `backups/compose.home.pre-joint-filter.yaml`. Before/after counts matched: 4 users, 20,590 current rows, 15,788 history rows, 2 mappings, 1 local file. Container healthy, zero restarts, LAN web HTTP 200. Immediate predecessor: `ted-home:20261015-home-watch`, also schema 41. Refresh the browser to load the updated UI.

### Previous Home/watch rollout

October 15 Home/watch update added 15% minimum automatic viewing, persistent native Home cache support and native-contract web watch validation/Mapped label. No server schema change beyond 41. Idle playback/server-transfer checks passed. The app was stopped for `backups/pre-home-watch-20261015.dump`; pg_restore archive inspection passed before replacement. Prior override: `backups/compose.home.pre-home-watch.yaml`.

Before/after counts matched: four users,20,543 watch-current,15,863 history, two mappings, one server-local file. Readiness and web HTTP 200 passed; container healthy with zero restarts. Tests did not mutate owner watch state. Native cache/local threshold requires the new Mac app; the agent did not open it.

The Home/watch predecessor was `ted-home:20261015-native-retirement`, also schema 41. Its backup is `backups/pre-retirement-20261015.dump`, with pre-retirement Compose files preserved. The schema 40→41 retirement rehearsal preserved 74 retained-table fingerprints in a private restored copy. Live retirement preserved accounts/history/mappings/files and four personal Stremio links. Recovery archives were retained, not deleted.

Returning across schema 41 retirement to `ted-home:20261014-startup-nextup` requires its matching database plus newer-state reconciliation, not an image-only rollback. Even restoring a recent backup may lose subsequent viewing: preserve current state before any rollback.

## Operations

From a home-server SSH shell:

```bash
cd /home/ptrschneider/projects/ted-teddy-preview
docker compose -p ted-teddy-preview -f compose.yaml -f compose.home.yaml ps
docker compose -p ted-teddy-preview -f compose.yaml -f compose.home.yaml logs --tail=100 app
docker compose -p ted-teddy-preview -f compose.yaml -f compose.home.yaml exec -T app ted healthcheck
```

The services restart with Docker. No public reverse-proxy/TLS setup was added; this is deliberate LAN HTTP. Use those same project/file flags in every backup/update command. Store backups privately; they include credentials/sessions/history. Never start a restored app next to live personal sync without preventing external effects.

The owner changed host DNS from unreachable 1.1.1.1 to router 192.168.1.1 after preserving `/etc/resolv.conf.ted-backup-20261014`. Do not overwrite that backup or silently undo DNS. `backups/compose.home.pre-ipv6.yaml` preserves the pre-egress override.

## Mac server identity and SSH

A forward retains the existing Teddy origin `http://127.0.0.1:33043`, preserving scoped profiles/downloads/pending reports after the server move. The server itself does not depend on the tunnel. Using the LAN URL instead creates a distinct local origin scope; it does not automatically move offline data.

Use the Tailscale SSH address `100.122.31.92`, including when the owner is away from home. The forward destination stays `192.168.1.2:33043` because that is the app port bound on the server; it is reached from the remote SSH process. Keep Teddy’s existing localhost origin.

After the SSH master exits or the Mac restarts, recreate it once:

```bash
ssh -MNf \
  -o ControlMaster=auto \
  -o ControlPath="$HOME/.ssh/ted-%C" \
  -o ControlPersist=12h \
  -o ExitOnForwardFailure=yes \
  -o ServerAliveInterval=30 \
  -o ServerAliveCountMax=3 \
  -L 127.0.0.1:33043:192.168.1.2:33043 \
  ptrschneider@100.122.31.92
```

Do not repeat while the forward owns 33043. Check `curl --fail http://127.0.0.1:33043/health/ready`. Agent shell operations use the same ControlPath; re-establish via owner login if the master expires. Do not guess passwords or weaken host-key checking.

## Retained migration and evidence

The Mac preview moved to this server on October 13/14. The stopped migration snapshot matched 79 public-table fingerprints and 1167 durable-file hashes at then-schema 37. These are historical migration results, not the current schema/counts. Migration backups `ted.dump`, `durable-files.tar.gz` and `deployment.env` remain under backups. Other existing server services were left intact.

All Colima VMs were deliberately stopped; inventory found one running profile (`default`) and helper processes. Disks/data were preserved. The Mac fixture remains `.devbox/test-evidence/2026-10-10T20-35-36.441Z-ted-native-test-89464-35183799`, migration evidence `.devbox/test-evidence/ted-home-server-2026-10-14`. Starting it now would introduce stale state and duplicate sync. Do not treat it as an active dependency or current rollback without reconciliation.

Latest deployment evidence is `.devbox/playback-cache-flush-20261020/deployment`; `.devbox/cache-range-debug-20261020` retains the live diagnosis/regression; `.devbox/playback-cache-scheduler-20261020/deployment` retains the preceding scheduler rollout; `.devbox/playback-cache-20261020/deployment` retains the initial migration/restore and scheduling diagnosis; `.devbox/indicator-polish-20261019` retains the previous artwork rollout; `.devbox/poster-fixes-20261019` retains the previous poster fixes; `.devbox/watchlist-ratings-20261019` retains the initial 0.2 rollout; `.devbox/preview-indicators-20261017` retains the earlier indicators rollout; `.devbox/preview-playback-20261017` retains the previous rollout; `.devbox/joint-filter-20261015` and `.devbox/home-watch-20261015` retain earlier evidence; retirement evidence is `.devbox/retirement`. The temporary home-server `ted-source-cache-test` database and its dedicated 37968 SSH forward were removed after those checks. The preview remains on 33043; CasaOS is separate on 33044.

Historical 14–22 second decode-command wall times included network/open/range/resume work, not demonstrated decoder computation. Subsequent network/startup/prefetch fixes were owner-accepted. Further opportunities are in [the startup review](../startup-and-account-review-2026-10-14.md); do not report old failure measurements as current native first-frame timings.
