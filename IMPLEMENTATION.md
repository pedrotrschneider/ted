# Engineering handoff — October 15, 2026

This is the current execution checkpoint. [PROJECT](PROJECT.md) contains agreed behavior, [AGENTS](AGENTS.md) engineering constraints, [README](README.md)/[native README](../teddy/README.md) workflows and [Operations](docs/operations.md) recovery. The older chronological implementation journal was consolidated to remove conflicting active instructions. Private pre-consolidation copies are under `.devbox/documentation-20261015`; current requirements do not depend on reading those copies or this chat.

## First-run web administrator setup — October 15

Empty installations now show Create administrator in the web UI. Public GET/POST /api/auth/setup use the existing cross-origin protection, bounded JSON input and password policy. POST reuses Bootstrap's PostgreSQL advisory lock and transactional empty-users check; once any user exists it returns setup_completed/409. Status/database failures fail closed. Creation returns to ordinary login, avoiding ambiguous account-creation/session-cookie coupling. CLI bootstrap remains supported. No migration or existing-account changes.

Validation: web typecheck/production build and all 114 frontend tests pass. TestWebSetupLifecycle and TestBootstrapConcurrentAndCannotResetExistingAccount pass with the race detector against a disposable PostgreSQL 18 instance on the home server. Covers empty/nonempty status, invalid input, cross-origin rejection, administrator login, closed setup and concurrent creation. Temporary database/tunnel removed afterward. Production/development databases untouched. Not deployed: CasaOS needs a newly published GHCR image and update before this UI is available. Browser visual acceptance remains manual.

## CasaOS production startup network repair — October 15

Production containers were created, PostgreSQL healthy, but app repeatedly failed migration before any schema existed. In-memory redacted configuration checks found matching app/DB password settings, but no shared network: CasaOS had assigned db to network_mode bridge while app used ted-casaos_default and ted-casaos_egress. Connected production db to ted-casaos_default with alias db and restarted only production app. Migrations completed, both services healthy, HTTP 200 on port 33044. Development untouched.

Persisted the network correction in /var/lib/casaos/apps/ted-casaos/docker-compose.yml (rollback file docker-compose.pre-network-fix.yml) and copied current Compose to /DATA/AppData/ted/docker-compose.yml mode 0600 for backups. Repository template now explicitly declares db networks: [default]. Installed password is still the sample placeholder; owner informed to rotate it before use. No password printed or changed. Source template Compose validation passed; production configuration validated through a private pipe because the file is root-readable only.

## CasaOS installation diagnosis — October 15

Inspected the home server over persistent SSH after failed imports. CasaOS app-management logs repeatedly report `Error response from daemon: error from registry: unauthorized` for ted-casaos; a direct manifest lookup from the host reproduces unauthorized. No production TED containers were created; development app/database remain healthy. Package visibility/access is the installation blocker; repository visibility does not prove GHCR package visibility. No credentials obtained and no services restarted.

CasaOS also warns POSTGRES_PASSWORD is unset during its repeated Compose interpolation, despite the escaped variable in the template healthcheck. Replaced that healthcheck with an exec-form local Unix-socket psql SELECT 1, avoiding shell interpolation while checking database readiness. Applies only to fresh CasaOS PostgreSQL initialization using the official image's local socket authentication. App database connections still use the configured password over the internal network. CasaOS message-bus service is active but /tmp/message-bus.sock is missing, causing event-notification errors; distinct from the fatal registry error, not repaired/restarted without further diagnosis. Existing preview Compose discovery emits missing-.env warnings, also unrelated to the new install's image-pull failure.

## CasaOS GHCR deployment template — October 15

Owner clarified the stable CasaOS installation must be independent; the current preview/database remains the development instance. deploy/casaos/docker-compose.yml now uses ghcr.io/pedrotrschneider/ted:latest, PostgreSQL 18, port 33044 (checked unused), project ted-casaos, and bind directories /DATA/AppData/ted/{postgres,metadata,media}. No named/anonymous data volumes. App UID/GID 1000, UTC and dual-stack egress retained. Two new-password placeholders; no existing database or credential reuse. README covers fresh setup, storing the filled-in Compose alongside data, stopped-stack filesystem backups preserving ownership, and matching-image restoration. No migration, stop, deployment or data change performed. Prior anonymous registry check was unauthorized; image availability/access must be resolved before install.

## Android lint JDK correction — October 15

Owner reports only Android still fails; the latest hosted log reaches url_launcher_android:lintVitalAnalyzeRelease and crashes inside lint's JavaDocParser with NoSuchMethodError for java.util.List.removeLast(). That API is available starting in Java 21, while the runner used Java 17. Updated Teddy's hosted JDK and Devbox package/lock to Java 21; retained Java/Kotlin target 17 and all lint checks. README/release guide now name JDK 21. Verified local Java/javac/keytool 21.0.11 and Gradle 9.6.1 launching and selecting its daemon JVM on Java 21; workflow lint and diff checks pass. Full Android lint/package build still awaits hosted verification.

The universal APK is shared by Android phones/tablets and Android TV: normal and Leanback launcher entries, optional touchscreen/Leanback features, TV detection and ARM32/ARM64/x64 packaging. This is packaging support, not device acceptance testing. Owner clarified the prior runner shutdown was exhaustion of free Actions usage, not proven memory pressure. Owner reverted the license changes; TED retains AGPL v3 and Teddy retains GPL v3. No license files changed in this fix. No commit/push/publication performed.

## TED hosted CI missing package — October 15

All three supplied Go jobs (behavior, race, vet) failed because `internal/config` was absent from the Git checkout. The unanchored `Config/` runtime-data ignore rule also matched the lowercase source directory on the owner's case-insensitive Mac; config.go and config_test.go existed locally but were never tracked. Root-anchor `/Config/` and `/Config-*/` now preserve private runtime-data exclusions without hiding the Go package. Both existing configuration source files must be included in the next commit; committing only .gitignore will not fix hosted builds. No runtime behavior or dependency changed.

Verification: configuration tests pass with the race detector, full Go vet and server build pass from a temporary export containing only Git-visible files (tracked plus nonignored new files). Verified root runtime data remains ignored and no other source files under internal/cmd/scripts/.github are ignored. The supplied hosted behavior/race logs show the other packages passing; no live database suite was rerun locally for this packaging fix. Hosted CI and GHCR publication await a new commit/push. No commit, push, deployment or production data change performed.

## Teddy hosted CI failure follow-up — October 15

The first supplied hosted logs exposed four independent failures. Linux ARM64 had no Flutter 3.47.0 stable SDK archive: the workflow now uses flutter-action Git installation at the exact 3.47.0 release tag (its `master` channel mode), not a moving development checkout. Official engine/Dart Linux ARM64 artifacts for that tag were verified available. Android setup requested Google's removed `tools` package: setup-android now explicitly requests `platform-tools`, followed by the existing pinned SDK/NDK/CMake installation. macOS 15 selected Xcode 16.4, whose SDK lacks connectivity_plus's NWPath.isUltraConstrained symbol: the runner is now macos-26 with an explicit SDK >=26 preflight.

Linux x64 reached tests but exposed a real TV auxiliary-control overflow. The right control lane now uses scale-down fitting only when needed, retaining centered transport controls and vertical alignment. A narrow-window regression reproduced the overflow before the fix. All 18 player-screen tests and all 357 native tests pass locally; targeted analysis, formatting, diff checks and workflow lint pass (with the existing narrow queue-key lint suppression). Hosted platform builds still need verification on a new commit; rerunning the old failed commit does not apply these fixes. Windows was still running when the owner supplied these logs, so no Windows failure is assumed. TED workflow failures are deferred at the owner's request. No commit, push, publication, deployment or app launch was performed in this follow-up.

## Teddy local Java toolchain — October 15

Initially installed `jdk@17` through Teddy's Devbox and updated devbox.json/devbox.lock; superseded by Java 21 for Android lint as recorded above. On the owner's Mac it resolves to Zulu OpenJDK 17.0.19. Verified `java -version`, `javac -version`, and `keytool -help` through `devbox run`; all resolve inside Teddy's Devbox profile rather than macOS's runtime-less system launchers. README documents re-entering an existing shell. No signing key was generated, no system Java changed, and no secrets were accessed.

## GitHub release automation — October 15

Added independent version.txt files (0.1) and matching tested tag allocators to TED/Teddy. Queued main/develop workflows reserve annotated v-tags, reuse the same version on reruns, reset patches by major/minor, and increment develop rc numbers for the next stable patch. PRs allocate only artifact versions. Failed builds can leave gaps. Old-run publication cannot move channel aliases/Latest backward past a newer reserved version.

TED's reusable existing web/Go checks gate AMD64/ARM64 GHCR image builds/publication; PRs export OCI archives. Docker now embeds the version file and release version. Teddy's matrix packages universal macOS, Windows x64, Linux x64/ARM64, and a universal phone/TV Android APK; no iOS. Platform artifacts/checksums merge into a draft, then publish only after all builds succeed. Published assets/images are left intact on reruns. Android release signing requires four repository secrets and fails closed without them; PR APKs are debug-signed. macOS remains ad-hoc and Windows unsigned. Android now has Teddy's application ID/label; it will install separately from inherited Plezy-ID builds. See [releases](docs/releases.md) for setup and recovery.

Implemented shared-major enforcement (including zero): public native version endpoint, request/response compatibility headers, server 426 before native route execution, client mismatch message without saving login credentials, and full release version in About. Missing headers mean pre-versioning major zero; route API v1 is separate. No app/runtime dependency added.

Verification: eight allocator tests pass including actual local bare-remote reservations/reruns/PR no-write behavior; workflow lint passes except its explicitly scoped queue-key suppression (actionlint 1.7.12 predates the documented GitHub queue property). Server compilation and two HTTP compatibility tests pass. Targeted Dart analysis is clean. The full native suite initially had 353 passing and two old header-expectation failures; both assertions now also verify the compatibility header. The affected playback/auth/settings rerun passed all 45 tests; a further bidirectional compatibility regression was added and the final playback/auth rerun passed all 42 tests. The first targeted invocation also named a nonexistent settings test file; the actual ted_app_settings_test.dart suite passed on the corrected run. Python scripts parse and the Mac shell script passes syntax validation.

Not yet verified: hosted GitHub runs, native Windows/Linux/Android builds, GHCR publication, or signed APK installation. No commit/push/release publication, credential provisioning, home-server deployment, or app launch was performed. The owner must configure Teddy's Android signing secrets and push the repos to main/develop to activate. Existing staged/imported repository files were preserved, not committed by the agent.

## Catalog scrolling and poster investigation — October 15

Movies/Shows/Search TV selection now updates a ValueNotifier consumed only by the spotlight. It no longer rebuilds the entire browse shell and reconstructs every catalog MediaItem/card on each focus move. Selection lookup compares stable IDs rather than constructing MediaItems while searching. Uncached spotlight enrichment waits 100 ms (owner's latest choice), with cancellation/stale-result fencing; already supplied metadata remains immediate. Pagination behavior is unchanged.

Targeted analysis is clean and 85 tests pass: catalog/Search/Home banner timing, keyboard and browse behavior, actual decoded artwork retry/resizing/recycling. A regression asserts the shelf widget remains identical across rapid focus changes while the spotlight changes. Real-image tests cover lazy-list remounting, memory-cache eviction, and held-arrow navigation through the actual Home shelf. They have not reproduced the owner's disappearing-poster report; do not claim that symptom fixed. Two Mac app processes were found running simultaneously: releases `20261015T181311Z-CMqSvEsv` and `20261015T182812Z-RrNY1j8S`. `lsof` confirmed both open the same cached-network-image Hive/lock paths, but with different inodes (each process retains a different version of the index). The cache dependency resets its index/files on HiveError; the reason for the observed replacement is not established. Shared cache contention is a hypothesis, not a verified cause. Owner confirmed titles remain visible and images stay blank after stopping. Owner was asked to quit both before testing one new build. Neither process was terminated by the agent.

Isolated macOS release build and signature verification passed: `/Users/ptrschneider/Projects/teddy/build/releases/20261015T184702Z-C7pWzrTy/Teddy.app` (203.1 MB). Not agent-launched. This build contains the catalog rebuild/debounce fix and regression tests, not a verified fix for the permanent blank-poster symptom. The owner subsequently reported everything working before requesting release automation; no further poster code change or verified root cause was established. No server deployment or owner-cache deletion was performed.

## Sidebar keyboard activation — October 15

Enter/select on sidebar destinations now hands focus into the page: first card of Home's first nonempty row (including when Continue Watching is absent), Search input, first Movies/Shows card, first Downloads entry or its empty toolbar, and Settings controls. Reselecting an open destination also performs the handoff. Right-arrow entry retains its distinct remembered-position behavior. Reload stays a current-page action; Account retains its focused popup. Pointer clicks do not trigger the keyboard-only handoff.

The handoff waits for asynchronous catalog/download data and checks focus ownership, input mode and the current route before applying; moving to another sidebar item cancels it. Initial Enter on a rail item explicitly announces keyboard input, including after mouse use. A scheduled frame handles reactivation when the existing page needs no rebuild. Downloads notifies readiness after scans and supports first-entry focus without changing ordinary remembered entry.

Verification: all 72 relevant tests pass across keyboard navigation, catalog, Search, Downloads tree, settings, focus recovery and browse/pointer behavior. Targeted analysis is clean. Existing assertions requiring an extra Right after Enter were updated to the new contract; new regressions cover Home first-row reset, delayed-load focus ownership and Settings activation. Downloads readiness can fire more than once because initial progress refresh requests another scan; its test checks readiness and actual first-entry focus rather than an incidental scan count. GUI acceptance remains pending. Isolated macOS build and signature verification passed: `/Users/ptrschneider/Projects/teddy/build/releases/20261015T182812Z-RrNY1j8S/Teddy.app` (203.1 MB). Not agent-launched; no server deployment needed.

## Page Up navigation — October 15

Fixed Teddy's top-edge callbacks left over from moving Reload/Settings/Account from the header into the sidebar. Search and catalog controls no longer fall back to Reload on Up; the desktop Home banner and Downloads toolbar no longer route Up to sidebar controls. Internal upward navigation (rows to filters, details to fixed Back) and sidebar Up/Down remain unchanged.

Verification: the new repeated-Up regression reproduced the original Search failure before the fix. The 39 Home/catalog/Search navigation tests and 13 Downloads/settings/focus tests pass; targeted Flutter analysis reports no issues. Tests cover desktop and TV Search/Movies/Shows top edges and explicit Left return to sidebar, plus Home banner Up and existing detail/sidebar behavior. An initial test invocation lacked the Apple SDK environment; rerunning through the existing child-only Apple environment resolved native-asset compilation. GUI acceptance remains for the owner. New isolated macOS build and signature verification passed: `/Users/ptrschneider/Projects/teddy/build/releases/20261015T181311Z-CMqSvEsv/Teddy.app` (203 MB). The agent did not launch it; no server deployment was required.

## Start here

- Source folders are now sibling `Projects/ted` (server/web/shared docs) and `Projects/teddy` (native client/build scripts). The owner removed the old Git history after backing it up, then initialized and pushed the sibling repositories for release automation. Both folders are now Git working trees; preserve current changes. The historical pre-removal baseline was 171f2af.
- Runtime: home-server Linux x86_64, PostgreSQL 18, server schema **41**. Teddy Drift schema **31**. Only the current Mac is owner-validated; broad T6 platform/release checks remain open.
- Installed server image: **ted-home:20261015-person-cursor**, ID `sha256:1c0e761c2db40c069a8b475dae4903ccb8261f9efe8a1bba609a329f8fb196f6`.
- Server source snapshot: `/home/ptrschneider/projects/ted-teddy-preview/releases/20261015-person-cursor`; transferred source archive SHA256 `808d84c4cd72f413da96bbb37b18a62f2bce89f749883f3e9a6111e9b5853bed`.
- New immutable Mac app: `../teddy/build/releases/20261015T142015Z-vVCwj0du/Teddy.app` (about 203 MB). Built/signed, **not launched by the agent**. Quit the older instance and open this build to use persistent Home cache/local 15% behavior.
- Latest docs were consolidated after the code artifacts were built; runtime code in those artifacts is the verified implementation. Test/comment/doc refinements after the snapshot do not change that runtime.
- Full deployment/tunnel/backups are in [the home-server record](docs/deployments/home-server.md). All Mac Colima instances remain stopped. The temporary `ted-source-cache-test` tmpfs database and its 37968 forward were removed after testing; the main 33043 forward remains.

## Person-page pagination fix — October 15

Cast loading succeeded, but opening a person failed because the joint-watch response wrapper in `internal/httpapi/native_metadata.go` serialized `next_after: ""` on the terminal filmography page. Teddy correctly requires an absent/null cursor or a valid UUID; even empty and single-page filmographies therefore failed, and larger filmographies failed on their last page before the row could publish. The native handler now uses a typed response with `omitempty`, retaining joint-account decorations and valid continuation tokens. No native code, provider matching or watch state changed.

The new HTTP regressions reproduce all three cases (0, 1 and 21 credits, covering TMDb/TVDB person identities and continuation ordering) before the fix and pass afterward. Affected metadata/filmography/season-badge behavior passed against disposable PostgreSQL 18 in 35.622 s; independent race checks passed in 52.208 s. Evidence: `.devbox/person-cursor-20261015` (original failures retained). No full native suite or GUI test was run; the existing strict client parser is unchanged. The isolated database and 37968 tunnel were removed.

Built and deployed `ted-home:20261015-person-cursor` after the owner stopped playback; the first deployment attempt correctly stopped at its active-playback check. Backup `backups/pre-person-cursor-20261015.dump`; prior Compose override `backups/compose.home.pre-person-cursor.yaml`. Schema 41 unchanged; before/after counts matched (5 users, 20,623 current, 15,806 history, 2 mappings, 1 local file). Health, zero restarts and LAN HTTP 200 passed. Immediate predecessor: `ted-home:20261015-folder-split`. No new Teddy build is required: reopen a cast member or select Retry in the existing app; biography and filmography should load. Owner visual confirmation remains pending.

## Local folder split — October 15

The owner requested separate sibling folders and removed Git history after backing it up. The old project directory was renamed to `/Users/ptrschneider/Projects/ted`; its native subtree moved to `/Users/ptrschneider/Projects/teddy`. Original top-level file/directory identities were verified after same-filesystem renames. Hidden files, ignored outputs, Config, Media and private recovery/evidence remain preserved. Mixed historical evidence stays under TED; Teddy-prefixed build state and the native check/build/Apple/loader scripts moved to Teddy. The loader fixture moved with its script.

Canonical AGENTS/PROJECT/IMPLEMENTATION and shared docs remain in TED; Teddy's AGENTS points there. Each folder has a Devbox manifest/lock using the existing pinned tools; native commands run from Teddy. No server deployment, application user data or signed app contents changed. Generated Flutter/native build metadata and historical evidence may retain old absolute paths; regenerate through the normal check/build scripts before building, never edit a signed release. The immutable app's new path is `../teddy/build/releases/20261015T044242Z-AROlXInm/Teddy.app` relative to TED. The server's remote deployment path is unchanged. Verification: both relocated Devbox environments resolve their tools; all four moved shell scripts pass syntax checks; the latest immutable app path exists; 44 relative documentation links resolve. The initial relocation checks did not rebuild the apps; the owner subsequently requested fresh builds and server deployment, recorded below. The native isolated-build copy excludes its now-local `.devbox` directory to prevent recursively copying build workspaces.

### Post-relocation build and deployment

From the new TED folder, the Go server build and production TypeScript/Vite build passed, together with all 112 web tests. The home server built `ted-home:20261015-folder-split` from the relocated source snapshot and reused unchanged runtime build layers. It was deployed after idle checks and a stopped-app database backup, `backups/pre-folder-split-20261015.dump`; the previous Compose override is `backups/compose.home.pre-folder-split.yaml`. Schema remains 41. Before/after counts matched: 5 users, 20,621 current watch rows, 15,803 history rows, 2 mappings, 1 server-local file. Readiness, healthy container/zero restarts and LAN web HTTP 200 passed. Immediate predecessor is `ted-home:20261015-joint-filter`. No owner watch edits were made for validation. Evidence is `.devbox/relocation-build-20261015`. The isolated native macOS release build also passed from the sibling Teddy folder, including dependency resolution/code generation, compilation and deep/strict signature verification. New app: `../teddy/build/releases/20261015T142015Z-vVCwj0du/Teddy.app` (203 MB), not launched by the agent. Native evidence: `../teddy/.devbox/relocation-build-20261015/macos.log`. Existing upstream Sentry deprecation and Libdav1d x86_64 linker warnings remain nonfatal. No native regression suite or manual playback check was rerun for this path-only change. Quit the old app and open the new immutable release for owner runtime verification.

## Web Watched account filters — October 15 follow-up

Movies/Series now reveal account toggles below Watched, initially all enabled. The active account uses white and direct joint accounts use membership colors with contrasting text. The server unions selected accounts' retained viewing before search/genre/Mapped filtering and pagination; duplicate titles appear once. An explicit empty selection returns no titles. Membership and disabled-account checks run inside the history query; arbitrary IDs, removed memberships and reverse host-to-member relationships cannot expose viewing. No watch mutations are performed.

The additive `/api/library-titles` contract returns `watch_accounts`; optional `watch_accounts` query IDs distinguish omitted/all, explicit subset and explicit empty/none. The browser cancels obsolete reads, restarts pagination when selection changes and validates returned account choices. Existing meaning of Watched (including retained/started viewing) is preserved.

Evidence is in `.devbox/joint-filter-20261015`: 112 web tests and the production TypeScript/Vite build pass. PostgreSQL behavior regressions pass (`go-2.log`), including movie/show union, overlapping accounts, retained history, Mapped/search/genre intersection, empty selection, membership/disablement isolation and a 101-title pagination boundary. Initial failures were caused by the test helper creating mappings for every fixture regardless of its publication flag; the corrected fixtures create unmapped titles explicitly. Initial logs are retained. Independent race checks pass (`go-race-2.log`, 107.285 s; behavior 70.728 s). The matched Go checks were `go test [-race] -p 1 ./internal/httpapi -run "TestLibrary|TestJoint" -count=1`, using disposable PostgreSQL 18. No full Go suite or native suite was rerun for this web-only change.

Deployed `ted-home:20261015-joint-filter` after idle checks and a stopped-app backup (`backups/pre-joint-filter-20261015.dump`). Schema remains 41. Before/after counts matched: 4 users, 20,590 watch-current rows, 15,788 history rows, 2 mappings and 1 local file. Healthy container, zero restarts, and HTTP 200 through the configured LAN bind passed. The initial host-loopback HTTP check failed because only the LAN address is published. The disposable test database and 37968 tunnel were removed; production forwarding remains. No owner watch-state edits were made for testing. Refresh the web UI, open Movies or Series and enable Watched; verify the colored account row and union/deselection behavior. Manual visual acceptance remains owner-performed.

## Previous Home/watch increment — completed implementation, owner UI check pending

### Minimum viewing qualification

`internal/watchstate/native.go` qualifies automatic viewing at position >= 15% of authoritative known runtime, falling back to reported actual duration when needed. Before qualification, immutable ignored receipts advance session ordering/lifecycle without writing watch-current/history/Recent/Stremio effects. Once qualified, backward seeks remain legitimate. Explicit terminal completion/Next and legitimate unknown-runtime natural EOF retain their established policy. The existing placeholder <= 30% of expected runtime guard runs first; completion remains 90%.

Teddy mirrors qualification in `TedLocalStore`: retain exact report envelopes and interrupted-session recovery, but `lastPosition` only exposes qualified sessions. Drift 31 adds a qualified latch and marks pre-existing retained resume sessions eligible so upgrade does not erase history. This threshold uses playback position, not accumulated wall-clock viewing. Manual watch edits, explicit Next and previously saved history are not retroactively suppressed/deleted.

### Persistent Home snapshots

New `TedHomeCache` persists the five raw row snapshots by server/account/device/row in Drift. `TedClient.home` hydrates cache before independently fetching personal rows and popular catalogs. Complete successes replace cache/UI, including empty results; failures/incomplete reads retain old snapshots. Generation/scope guards discard obsolete results. Cache I/O failure does not prevent network use. Both client constructors supply the cache and profile Sign Out purges its scope.

Home removes per-row Loading labels and suppresses the indeterminate loading bar when cached/loaded content exists. Legitimate card progress bars remain. The first uncached load may still show one progress line. Watch snapshots remain display-only and never authorize playback/mutations.

### Web watch-state error and label

The browser validator still required the obsolete integration `sequence` field, which native watch responses no longer return. `web/src/title-watch-api.ts` now validates the current ID/flags/error/revision/version contract without sequence. Movie/episode read-edit regressions include native payloads without it and malformed-state rejection. This fixes controls being locked by the misleading incomplete-watch-state message; it does not hide genuine malformed responses. Mapped replaces Saved in browse controls/headings.

### Verification

Evidence is private under `.devbox/home-watch-20261015`:

| Check | Result/scope |
| --- | --- |
| Go watchstate behavior | Pass, full package against disposable PostgreSQL 18, 176.453 s (`go-watch-2.log`) |
| Go watchstate race | Pass, independently run full package, 258.646 s (`go-race.log`) |
| Affected HTTP/add-on boundaries | Pass, httpapi 23.339 s/addons 21.799 s (`go-boundaries.log`): native resume, rewatch, state edits, source/history/Recent boundaries |
| Web | 109 tests pass and production TypeScript/Vite build (`web-2.log`) |
| Native static analysis | No issues for lib/test |
| Native TED suite | 350 tests pass (`native-all.log`) |
| Focused cache/offline/upgrade tests | 28 pass (`flutter-2.log`) |
| Final cache/browse check | 20 pass (`native-cache-verified.log`) |
| macOS release | Isolated universal build/signature verification passed (`macos-build.log`) |
| Server release | Image built on home server; idle check, stopped-app backup, update, health and data-count checks passed |

Meaningful regressions cover 14,999 versus 15,000 ms of a 100,000 ms runtime, immutable duplicate ignored receipts, qualified backward seeking, offline preservation of a prior explicit decision, successful-empty cache replacement, failure retention, held-refresh immediate hydration, scope isolation/sign-out purge and schema 30→31 preserving downloads/reports/resume. Existing zero-position fixtures were updated only where their old immediate-start assumption conflicted with the new approved threshold; the zero-start behavior now has an explicit absence/qualification assertion. Initial failures remain in evidence. A spinner assertion was corrected to distinguish indeterminate loading from legitimate watched-progress bars, not to bypass a UI failure.

This increment did not rerun every Go package or the entire upstream Flutter suite. The retirement checkpoint below did broader checks. No new manual GUI playback, live Stremio writes or non-Mac runtime coverage is claimed. Owner verification steps are in the native README.

Deployment preserved four users, 20,543 watch-current records, 15,863 history records, two mappings and one server-local file. Backup: `backups/pre-home-watch-20261015.dump`; schema unchanged at 41. Post-start readiness/HTTP 200 and zero restarts passed. No owner watch state was changed to test this release. The prior image and backup remain available; an image-only rollback to the immediate schema 41 predecessor is distinct from rolling back across retirement.

## Delivered baseline and acceptance limits

| Area | Current state |
| --- | --- |
| Foundation/M1–M4 | Accounts, discovery, source-first RD mapping, batched validation, server local copies, durable personal state and full-account Stremio flags implemented. Owner accepted core workflows in the prior setup. |
| T1–T3 | Independent TED lifecycle, opaque native auth, direct mapped playback, catalog/search/personal rows implemented. Mac owner confirmed login restore/refresh, playback/seek/resume/search/watch edits. |
| T4/T4b | Add-on/RD playback and Plezy-based native UI/player integrated; owner accepted current Mac experience through subsequent fixes. |
| T5 | Device downloads, offline metadata/artwork, durable exact-report replay and restart recovery implemented. Owner confirmed disconnected playback, reconnect CW, offline restart/resume and paused-download restart. |
| T5b | TMDb/TVDB lazy metadata, provider settings, cast/person fallback and artwork contracts implemented; backend handoff accepted. |
| T5c | Home/Movie/Show/Search/details/person/downloads/settings/sidebar/player/source/profile/joint-account iterations implemented. Home and improved sources/startup/prefetch explicitly owner-accepted. Latest small changes retain owner visual-check gaps. |
| External integration retirement | Implemented/deployed October 15, schema 41. No active external media-server routes/workers/native application path. Preserve required migration/legal/recovery identifiers only. |
| T6/M5 | Not complete: non-Mac platforms, broad codec/device coverage, coordinated latest restore, diagnostics/maintenance/release hardening and hosted CI remain open. |

Do not reopen superseded staged plans as pending work: per-user add-on discovery, earliest-gap Next Up, focus-activates-sidebar, left-side watch badges, background downloads under inactive profiles, direct-URL-only add-ons and completion-only person Watched are no longer the contract. Neither are every-hover scroll-to-top, unbounded eager artwork alternatives or forced login on every auth failure.

### Retirement evidence

The preceding schema 41 release removed retired integration routes/workers/persistence and unused upstream application/integration paths while retaining shared player/widgets/settings and mandatory notices. The app runs without external profile/config keys. Retained-table upgrade rehearsal used a private copy of actual schema 40 data with **74 table fingerprints unchanged**, with no workers/provider sync against that copy. Protected private source/DB recovery evidence is `.devbox/retirement`.

That checkpoint passed the full Stremio behavior suite, focused independent race tests for shared-account locking/enrollment/replay/failure recovery/rewatch chronology, relevant server/web checks and the complete retained Flutter suite (**2719 passed,6 platform skips**). Its isolated Mac release is `../teddy/build/releases/20261015T035433Z-ylQKQNpt/Teddy.app`. These results are historical to that code checkpoint; they are not a claim that every suite was rerun after the 15% feature. The upgrade/live rollout preserved accounts/history/mappings/local files/personal links. Current release adds no server migration beyond 41.

## Code map for the next model

| Responsibility | Entry points |
| --- | --- |
| Server startup/config/jobs | `cmd/ted`, `internal/config`, `internal/database`, `internal/maintenance` and domain worker registrations |
| Local/browser/native accounts | `internal/accounts`, `internal/httpapi/sessions.go`, `internal/httpapi/joint_accounts.go` |
| Native token family rotation | `internal/accounts/native.go`, `native_test.go` |
| Joint read projections | `internal/accounts/joint.go`, `internal/httpapi/joint_watch.go` |
| Catalog/rows | `internal/cinemeta`, `internal/watchstate/native_rows.go`, HTTP client catalog/row handlers |
| Native watch intake | `internal/watchstate/native.go`, `native_threshold_test.go`; offline ingestion and explicit local edits alongside it |
| Personal Stremio sync / server discovery | `internal/stremio`, `internal/addons`; separate connection ownership/credentials |
| RD account inventory / pinned resolution | `internal/realdebrid/inventory*.go`, `internal/streaming` |
| Metadata/artwork/person | `internal/metadata`, provider adapters/settings, metadata HTTP handlers |
| Browser watch parsing/filtering | `web/src/title-watch-api.ts`, `web/src/MediaBrowser.tsx`, `web/src/discovery-filters.ts` |
| Teddy shell/profiles | `../teddy/lib/teddy_main.dart`, `../teddy/lib/ted`, shared navigation widgets under `lib/widgets` |
| Teddy server access/cache | `../teddy/lib/ted/ted_client.dart`, `ted_home_cache.dart`, source-cache implementation |
| Native durable local state | `../teddy/lib/database/app_database.dart`, `ted_schema.dart`, `../teddy/lib/ted/ted_local_store.dart` |
| Native offline transfer/planning | `../teddy/lib/ted/ted_downloads.dart`, show-download/source-planning helpers |
| Settings/defaults | `../teddy/lib/ted/teddy_settings_screen.dart`, `teddy_settings_actions.dart`, `../teddy/lib/services/settings_service.dart` |
| Regression suites | `internal/*/*_test.go`, `web/tests`, `../teddy/test/ted` and retained widget/player tests |
| Builds/tests/operations | `scripts/check*.sh`, `test-database.sh`, `../teddy/scripts/build-teddy-macos.sh`, `../teddy/scripts/teddy-apple-env.sh`, Compose/Dockerfile |

Confirm actual filenames when extending a path family; do not infer unlisted behavior merely from a class or test name. Auth/session/source/cache layers have separate invalidation scopes. Do not introduce duplicate business rules in UI handlers.

## Important retained implementation decisions

- Server add-ons and personal Stremio sync were split; migration reused native_tester's authorized external account in both roles. Joint membership is read-only, nonnested and separate from indirect shared-Stremio semantics. Token-family/storage serialization and profile switch fencing are not optional simplifications.
- Browser logout was corrected to current-session only in schema 40; password/disable/all-device actions still revoke globally. Do not restore old all-session logout examples.
- Who's watching appears every launch; fixed startup destination follows selection. Active-profile-only work supersedes the earlier request to keep inactive downloads running. Sign Out intentionally purges local reports/copies; ordinary switching/removal does not.
- Next Up anchors by most recent legitimate completion and skips watched successors; it is neither earliest-gap nor furthest-ever completion. New Releases uses account creation only for undated release comparison. Historic one-off owner date edits (JoJo S6E3 June 5, 2026; Yellowjackets S3 October 13, 2025) were authorized for the then-current account, not a repeatable migration or a license to rewrite dates again.
- Shared Recent/Favorites use binge groups; local last-season choice and twelve-hour source cache are separate. All/favorite/recent visual categories do not duplicate streams. RD direct preparation never silently falls back to another release/add-on URL.
- Stable file selection, range/placeholder validation, explicit Next terminal reporting and seek/resume completion guards are safety boundaries. Preparation/prefetch does not count as viewing.
- Source/stream reliability improved after the home-server dual-stack egress correction, canceled-operation isolation and source-cache/preparation changes. The owner confirmed fast playback and near-instant prefetched Next. Do not mislabel prior network/open timings as decoder CPU time.
- Download root migration uses a journal; bulk downloads stage before queue commit; concurrency admission excludes stale finished snapshots. Existing copies/reports must survive failures. Downloaded completion is relative to download time; normal server flags are not reset.
- Shared card padding should not be stripped globally. Native first-row/banner behavior is based on first visible row, not hardcoded Continue Watching. Pointer hover never changes focus/scroll; only keyboard/remote reveal does.
- Metadata field names/provenance are compatibility details, not an active integration. Character images are TVDB role-specific when available; TMDb profile images must not masquerade as characters. Person viewed/completed flags remain separate.

## Known gaps and deferred work

1. **Settings default discrepancy found during this audit:** inherited `_SkipMarkerModePref` currently resolves to Button, while approved intro/credits default is Off. `themeMode` resolves to OLED on TV, while the approved default is System. No native initialization override was found. Existing explicit user values must be preserved when fixing. This audit documented the discrepancy without mixing an unrelated settings behavior change into the release.
2. **Latest owner checks:** open the new Mac build; verify cached Home/no row-loading text, below 15% exclusion and qualified backward seek, web watched edits, Mapped label and the new account toggles/union results. Automated checks pass, but agent did not operate the GUI.
3. **T6:** Windows/Linux/Android mobile/TV runtime, secure-storage behavior, codec/player/range/reconnect/offline/download-root recovery on those systems, Intel/older macOS and optional later iOS. Private release/distribution only; no store/notarization claim.
4. **M5 operations:** consolidated health/failed-delivery diagnostics, documented/tested restore against mismatched server/client checkpoints, agreed retention for diagnostic records and release hardening. Preserve replay/history protection and private backups. The latest complete OS/media/client restore drill remains unverified.
5. **Performance:** further startup improvements are documented in the dedicated review and remain investigation-only. Re-measure same file/cut/resume/CDN with first-frame timings before optimizing. Persistent cross-session byte caching is low priority by owner choice.
6. **Deferred features:** More like this; archive-member RD streaming (Reborn exploratory result only); Prowlarr/other debrid providers; watchlists/monthly metadata expiry (personal notes, not implemented requirements); non-artwork descriptive overrides; automatic shared-media eviction; React/Capacitor; public stores.
7. **CI/repository:** owner removed/backed up Git history before splitting the projects; no current repository or hosted CI result. Preserve the working folders before initializing new repositories. Private build/evidence snapshots contain secrets and must not be committed.

## Suggested continuation procedure

Retained deferred findings: a More like this row could use provider recommendation/similarity lists or local genre/cast/production/release metadata, but ranking is not proof of relevance. Lazy caching of recommendation IDs with bounded freshness and last-success retention could accommodate new releases; permanent caching would become stale. The owner explicitly deferred the feature; no adapter, row or cache policy was selected. The Reborn archive experiment found an RD-delivered RAR despite an underlying video file list; a first stored member and byte-range support suggested feasibility, but indexing, identity validation, renewal and actual playback remain unimplemented/unverified. Do not mistake either investigation for a supported source path.

Documentation audit: current project-authored guides were consolidated, account/startup proposals reconciled, historical prototype docs explicitly labeled unselected, and upstream legal/package history retained. Twenty-two remaining Markdown documents were inspected for links/fences (78 relative links valid); fourteen shell examples passed syntax checks without execution. Backup/session invalidation examples were checked against the current schema. Private original prose is retained under `.devbox/documentation-20261015`; it is historical evidence, not an alternate specification.

Read this checkpoint and PROJECT first, inspect the current files without assuming a Git repository, then choose the owner's next requested scope. Use Devbox. Do not start the retired Mac server/Colima or duplicate personal sync. If database tests are needed, create a fresh isolated PostgreSQL 18 target and run behavior/race independently with packages serialized. Never use production as TED_TEST_DATABASE_URL.

For a server change, check idle work, create a stopped-app backup, retain the previous image and use the existing home-server Compose/network configuration. For a Mac change, build a new immutable release and provide the path; owner opens it. Record actual tests/artifact hash/deployment separately from pending manual acceptance. Update canonical docs in place rather than append another contradictory chronology.
