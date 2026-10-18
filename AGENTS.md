# Engineering guidance

Read [PROJECT.md](PROJECT.md) before changing behavior and [IMPLEMENTATION.md](IMPLEMENTATION.md) for the current checkpoint, code map and verified limits. [README.md](README.md) and [../teddy/README.md](../teddy/README.md) are runnable workflows. The owner's later instructions supersede historical proposals. A specification, build or successful API call is not evidence of end-to-end behavior.

Shared agent documentation lives in the TED folder. Teddy is the sibling `../teddy` project, with its own Devbox entry point and native scripts; its AGENTS.md points here.

## Scope and decisions

TED/Teddy are independent of external media servers. The owner approved removing the old integration and isolating shared Plezy widgets/player/settings from unused upstream applications. Preserve TED accounts, catalog identities, mappings, downloads, metadata/artwork, history, historical migrations needed for upgrades, mandatory third-party notices and private recovery archives. Do not treat archived owner data as a dependency or delete it during cleanup. Do not revive old bridge requirements from historical migrations or evidence.

Selected architecture: Go/PostgreSQL 18/pgx/Goose/River/SCS/Argon2id; standard-library HTTP/JSON; React/TypeScript/Vite served by Go on the same port; FFmpeg/ffprobe; Teddy Flutter/Dart/Plezy native player. Compose runs app and database. Private builds target macOS, Windows, Linux, Android mobile/TV; iOS is later desirable. Mac acceptance is not broad platform validation.

Consult the owner before choosing a new library versus custom implementation. Research runtime/deployment dependencies, licensing, maintenance, cancellation, failure behavior and architectural fit first. Existing explicit decisions remain authorization; do not repeatedly ask. No general plug-in framework, microservices, Redis, separate broker/frontend deployment or new UI/query-cache library is approved. Preserve upstream licensing; TED's AGPL does not replace dependency notices.

## Workflows and implementation

- Work incrementally around concrete responsibilities: accounts, catalog, metadata, sources, watch state, jobs, native transport, offline storage and presentation. Separate domain rules, persistence and external effects; API handlers/workers share workflows.
- Keep adapters' protocols/quirks behind small named contracts. Prefer composition and explicit dependencies to catch-all optional types or hidden global state. No speculative abstraction.
- River owns durable background jobs, retry state, progress, cancellation and restart recovery; goroutines execute work. An in-process mutex alone is not durable ownership or cross-process serialization.
- Treat filenames, URLs, provider responses and descriptions as data. Invoke FFmpeg/ffprobe with explicit argument arrays, not shell templates. Validate paths/ranges/identities before effects; support cancellation and bounded operations.
- Preserve authorization, failure ordering, persistence and public behavior during organizational refactors. Do not mix unrelated feature changes or silently discard malformed state/recoverable work.
- Do not add compatibility layers for obsolete prototypes without a concrete upgrade requirement. Preserve recovery of the current format and historical migration identifiers.
- No computer-control/computer-use tools unless the owner explicitly asks. General implementation/verification authorization does not authorize operating their GUI. Prepare builds/CLI state and give manual checks.

## Development and releases

Use `devbox run` or `devbox shell`. Tools belong in devbox.json/lock; application dependencies belong in their manifests/lockfiles. Document environment changes. Starting Docker/database services must be explicit, never shell initialization. On this owner's Mac all Colima instances are intentionally stopped; use the authorized home server or an explicitly isolated test service, not an automatic local VM restart.

Mac builds use full owner-provisioned Xcode, Devbox Flutter/CocoaPods/Ruby/GNU rsync and `../teddy/scripts/teddy-apple-env.sh` child-only Apple SDK/compiler selection. Do not change global xcode-select or the Nix store. Build with `../teddy/scripts/build-teddy-macos.sh` from isolated source copies, publishing unique immutable app paths. Never replace native libraries in a running app: that can load a second SQLite instance and crash on shutdown. Preserve earlier releases until no longer used.

All durable server state uses explicit host bind-mounted Config/media files, never Docker-managed named/anonymous volumes or container layers. Backups include the database, metadata, media, configuration and retained recovery data. Use PostgreSQL 18-compatible stopped copies or logical backup/restore; never replace an existing DB with an empty bind directory. Test restores independently with copied jobs prevented from writing externally. Older snapshots require session invalidation before exposure and reconciliation of newer viewing/pending reports; preserve token replay lineage.

## Authentication and account boundaries

- Local TED usernames/display names/password policy remain authoritative. All signed-in users may play all native items; no viewer/item allowlist. Destructive shared-media actions/provider credentials require administrators.
- Native opaque access/refresh tokens use standard randomness/digests and the approved ten-minute/thirty-day/ninety-day defaults. Retain the family ID, one valid refresh per family, transactions/constraints, spent-token replay detection and account revocation. Never use refresh tokens or plug-in secrets on media routes.
- Browser SCS sessions are separate. Ordinary browser logout revokes only that session with late-save protection. Explicit all-device logout/password changes/disablement remain account-wide.
- Teddy uses flutter_secure_storage 10.3.4 for one OS-backed profile bundle. Serialize refresh and actual storage writes, verify persistence and set a pending marker before rotation. A timeout does not roll back an OS write; uncertain refresh/save requires sign-in. Never use preference obfuscation or independent isolate refreshes. Linux requires libsecret/unlocked storage. Recover denied Keychain access through explicit retry without reporting every storage failure as permission denial.
- One administrator-managed Stremio connection supplies server add-ons; only personal history links enqueue watched sync. The owner approved reusing native_tester's existing external account for both during migration, not coupling their lifecycles.
- Joint memberships grant only read-only watch projections; no login/write authority/persisted history merges/nesting. Members can leave and set their own badge color; hosts remove members. Shared external history accounts are allowed with an explicit indirect-sync warning.
- Teddy has one active authenticated profile. Switching pauses/fences work and preserves scoped data. Explicit Sign Out purges only that profile's credentials/downloads/caches/pending reports, preserving other profiles, installation ID and server history. Offline identity is local playback only. Do not automatically discard downloads on auth expiry.

## Source identity, streaming and downloads

- Stable kind/title/episode IDs survive streaming, downloads, remapping and local-copy removal. Never derive identity from paths/URLs; no mandatory normalization/remux/stubs. Browse/history import cannot create mappings or downloads.
- Source priority: validated local copy, mapped RD, explicit add-on choice. Add-on torrent identity order is infoHash/fileIdx, recognized Torrentio/Comet binge-group extraction, then direct HTTP(S) only without identity. No private-URL hash extraction. No silent fallback after identified-release failure, guessed title associations, local P2P, archives or HLS/DASH proxying.
- RD adds select only the specific requested file, never the whole pack first. Reuse ready exact-file links and preserve existing originals, selections and pinned file identity. Shared inventory uses durable minute incremental checks/daily full reconciliation, last-complete preservation and credential fencing. Ready badges require a ready selected file, not just a hash.
- Stable TED playback URLs remain authenticated. Pin source/file versions through ranged delivery/link renewal; new requests need fresh access credentials. Streaming body delivery is separate from bounded JSON middleware. Keep credentials/private provider URLs out of clients/assets/logs.
- Shared Stremio Favorite/Recent preferences are independent of personal history, private configurations, mappings and local season preference. Match add-on/binge group in a show, otherwise add-on/name in exact media. Record at most three recents on qualified actual viewing with original timestamps; verify offline account/device/media provenance. Disablement is server-wide by manifest ID/public variant, fencing lookup/selection/renewal without deleting preferences.
- Stage/validate outside active paths on the destination filesystem; publish atomically only after success. Preserve the old source until replacement validates. Coordinate active playback/transfers/removal; database transactions do not make filesystem or remote writes atomic.
- Mappings use sequential atomic batches (200 default) and four measured validation workers. Completed batches survive later failure. Fence cancellation/stale workers, preserve failure diagnostics and retry unfinished batches. Explicit remapping may interrupt playback; owner accepts cut/offset differences.
- Delete only owned files. Keep tombstones so discovery cannot recreate removals; preserve personal history. Daily cleanup removes only unreferenced owned files, not mapped copies or arbitrary archives.
- Native immutable live/offline reports retain event IDs, sequences, timestamps and server/account/device scope in Drift until validated receipts. Local media removal preserves reports; explicit Sign Out is the sole approved purge exception. Installation device ID survives logout.
- Bulk downloads stage a complete source plan before atomic queue commit. Cancel discards all staged choices; Skip remaining commits chosen entries. Carry only the explicitly selected RD strategy or unique add-on/binge group. Journal validated failed-source replacements before resetting failed bytes; preserve active/completed copies and watch/replay state.
- All download entry points share the live concurrency limit. Lowering drains active jobs; stale queue snapshots cannot re-admit completed jobs. Pause/removal/root migration/shutdown fence admission. Root relocation uses durable verified-copy recovery and activates only after validation. Range size defaults 512 MiB; max simultaneous defaults 1 (1–10).

## Viewing and metadata invariants

- Automatic live/offline viewing qualifies at playback position >=15% of known duration. Before that, retain ignored replay receipts but publish no history/resume/Continue Watching/viewing Recent. Once qualified, backward seeking retains progress. Unknown runtime qualifies only on legitimate natural completion; explicit watched/Next remains independent. Preserve previous saved history rather than retroactive cleanup.
- Completion is 90%, separate from qualification. Known-duration early EOF/buffering/seek/resume signals are interruptions, never natural completion/advancement. Actual duration <=30% of known expected runtime cannot create viewing, Recent or a published device download. Unknown runtime is not zero-length completion.
- Explicit Next completes at any offset through a distinct terminal durable report with stale-event fencing. Keep current completion, progress, history, dates, observations and outgoing acknowledgments separate. Failed/partial reads are not absence/unwatch. Ordinary unwatch retains history; explicit Never watched is a scoped confirmed action.
- Continue Watching: one movie/show, most recent qualified unfinished episode before pagination, original viewing timestamps and numeric tie breaks; include specials/rewatches and preserve other progress. Next Up: latest completed/marked regular anchor, first released unwatched successor, skip watched successors and earlier gaps, exclude shows in Continue Watching and future/unknown/special candidates. Known action times outrank initial import fallback; preserve offline chronology.
- New Releases compares the Next Up candidate's release day against the anchor watch day, or account creation when undated. Never fabricate dates/change the anchor. Schedules/counts use server calendar day, released main-season episodes; partial progress is not completion. Show watch/total pills at top right, white for active account and chosen joint colors.
- Stremio sync is whole-account flags plus optional validated completed-episode recency; no resume/play-count fabrication. Poll distinct accounts every 60 seconds, persist snapshots, serialize writes, read immediately before put, preserve unrelated fields and verify afterward. Owner accepts residual external concurrent-write risk; do not claim atomic conditional protection. Confirmed absence may create temporary history entries; failures cannot.
- Cinemeta owns discovery/identity/IMDb/schedules. TMDb movies and TVDB shows/seasons/episodes enrich lazily through standard adapters/River. Use IDs/unique tuples, not name/date equality. Person biography fallback may use verified TMDb/IMDb IDs; best-effort name match is approved only without either ID, recorded as heuristic and never merging identities. Fetch biographies only on person access.
- Other-user indicators show enabled non-joint accounts with avatars/initials. Project each person’s own viewing plus enabled joint accounts they belong to, excluding joints already represented for the viewer (their own account or shared memberships). Deduplicate episodes before counting. Stack people vertically within the artwork, shrink only to a readable minimum and reserve a final +X badge for hidden people. Keep the viewer’s personal/direct-joint badges, filters, resume and Person Watched separate; these unions never mutate history.
- Person Watched includes qualified started/retained viewing; one episode suffices for a show. Keep completion flags separate. English titles/characters remain consistent; character images fall back to posters.
- Artwork editor exposes one Primary/Logo/Banner/Backdrop and one poster per season. Load metadata on access if absent; fetch alternatives on demand, retain old usable files on failure and overrides through refresh. No general descriptive editor is approved.
- Keep original audio language outside translated descriptive DTOs. Local override→original→English→default for audio; override→English→default for enabled subtitles. Downloads retain this metadata for offline policy.
- Persist five Home rows per server/account/device; render cache while independent refreshes run. Complete empty success replaces cache, failures/partial data retain it, late prior-context responses cannot publish. No row-loading text. Source-results cache is separately twelve hours and never authorization.

## Interaction invariants

Use “series” for customer-facing media labels and new or touched TED-owned code names. Preserve third-party protocol names and stable existing payload/storage values unless an explicitly versioned change is required.

Mouse hover is visual only: never request focus, lock row selection or scroll. Keyboard/remote focus reveals controls; deferred scrolling rechecks input mode. Mouse click/wheel/drag remain normal. Pointer motion may reveal player controls, not transfer keyboard focus.

Sidebar activation requires Enter/click. Current page and keyboard white-outline focus are independent; collapse width is the same TV/desktop. Return from sidebar to the remembered row; held Left stops at the first item until a fresh press. Home's first visible row reveals the hero with bounded row visibility; Up there does nothing.

Details/settings keep the sidebar and fixed Back. Episode→season→show→caller flow applies even from Home. Escape dismisses the active dialog first and otherwise follows that flow. Player Back restores current target details and usable focus. Settings use one Plezy-style page; reset/import/cache clearing preserve authentication/device identity/history/downloads/reports and exclude paths/credentials from portable backups.

## Verification and delivery

Test observable contracts at their owning boundary. For bugs add two or three meaningful regressions proportional to scope, covering triggers/failure boundaries, not incidental strings/files. Concurrency tests use held locks/coordinated workers/failures and real persistence. Serialize Go packages sharing a PostgreSQL database (`-p 1`) because advisory locks are database-wide; preserve concurrency inside tests.

Run relevant behavior/race checks independently, frontend type/build and native checks. Never hide failures with retries/skips/relaxed assertions/timeouts without establishing cause. Once checks pass, repeat only for new changes/unresolved issues. Keep useful failure logs and identify the tested working tree/snapshot, environment and unavailable checks. CI workflow files are not a hosted CI result; TED and Teddy now have separate Git repositories and release workflows.

Verify external integrations against deployed versions when in scope. Mocks/builds/API success do not establish playback, seeking, offline recovery or another platform. Distinguish automated isolated checks, read-only live measurements, owner verification and pending manual acceptance. Do not claim broad coverage or silently mutate owner history as a test.

Keep logs redacted, bounded and useful; failures/actionable job phases remain visible without verbose mode. Status polling cannot determine correctness. Optimize from measured phases; do not weaken validation, authorization, locks or recovery. Further startup improvements are documented/deferred until requested.

Maintain docs with work: PROJECT for requirements, IMPLEMENTATION for checkpoint/evidence/gaps, README/native README for runnable operations, deployment doc for installed artifacts. Remove stale active instructions instead of appending contradictory corrections. Historical private backups are not a substitute for a self-contained handoff. Preserve mandatory upstream notices/changelogs and owner personal notes.

Do not create documentation-only commits unless the owner explicitly requests one. Include relevant documentation in the same repository's code commit; otherwise leave documentation changes uncommitted until a relevant code change. TED-only work must not trigger a Teddy commit, push, tag or release solely to synchronize documentation. Prefer TED's shared documentation for cross-project server behavior.

Report changes, reason, checks and limits; include actual artifact paths/manual actions and expected behavior. Do not invent commands. For a requested commit message use a fenced text block: short summary, blank line, grouped bullets; omit docs from code-commit messages.
