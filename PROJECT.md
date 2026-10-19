# The Entertainment District — product contract

Updated October 19, 2026. This is the current agreed direction, not an execution log. Later owner decisions have been reconciled here; superseded alternatives are not active requirements. Read [IMPLEMENTATION.md](IMPLEMENTATION.md) for delivered code, verification and gaps, [README.md](README.md) for operations, and [AGENTS.md](AGENTS.md) before engineering work.

## Product and architecture

TED is a self-hosted media discovery, source mapping, download and personal viewing-history server. Teddy is its native Flutter/Dart client, derived from Plezy. TED owns identity, accounts, metadata, playback authorization and watch state. The responsive React web UI is the management interface. Both connect directly to TED; no external media server, bridge, plug-in or external login is required or supported.

The October 14 retirement decision removes the old integration and unused upstream application paths. Preserve user accounts, catalog IDs, mappings, downloads, artwork, viewing history, immutable migrations required for upgrades, mandatory notices and private historical recovery archives. Those archives are owner data, not active dependencies. Do not delete them as source cleanup or make their import a playback prerequisite.

Selected stack: Go, PostgreSQL 18, pgx with explicit SQL, Goose migrations, River persistent jobs, SCS browser sessions, the alexedwards Argon2id wrapper, standard-library HTTP/JSON adapters, React/TypeScript with Vite, FFmpeg/ffprobe, and Flutter with the retained native player. Go serves the built UI and API on one application port; Compose runs app and database. No Redis, microservices, external identity provider, general plug-in framework, frontend component library or query-cache dependency is selected. Consult the owner before new library/custom-implementation decisions.

Required native targets are macOS, Windows, Linux, Android mobile and Android TV. iOS is desirable later. Distribution is GitHub Actions releases/sideloading and GHCR images, not app stores; see [release policy](docs/releases.md). During the 0.x alpha, both products share the required MAJOR.MINOR compatibility version; breaking changes increment the minor. After 1.0, major versions define compatibility. Patch releases remain compatible. The 0.2 watch-list/rating fields are core required fields, with no 0.1 fallback. Mac owner acceptance does not establish the other platforms. TED is AGPL-3.0; retain Plezy GPL-3.0 provenance and all dependency/native notices.

Customer-facing media terminology is **series**, including singular usage. Prefer series in TED-owned code names too. Preserve third-party protocols and existing persisted/API identifiers where renaming would create unnecessary compatibility work; additive optional payload fields do not require a compatibility bump.

## Accounts, discovery provider and profiles

### TED authentication

- Accounts are local TED accounts with unique login usernames and editable display names. Passwords are optional by default; enforce the configured minimum, not an invented policy. Administrators manage users, provider credentials and destructive shared-media operations. All signed-in accounts may browse and play all native titles; do not add a viewer/item allowlist.
- Each account can upload its own profile picture from the pencil at the bottom left of the web Your account avatar. Accept JPEG, PNG, WebP and GIF up to 20 MiB/40 megapixels, keep the first frame and center crop to a 256-pixel square. Store the normalized image with the account; failed replacements retain the previous image. Teddy uses it in the profile chooser and sidebar, with the existing initial as fallback. Its scoped local cache supports offline display; only the selected authenticated profile refreshes, on entry/Reload and at most once a minute during ordinary use. Switching preserves cached pictures; explicit Sign Out purges only that profile's cache.
- Browser cookies and native credentials are separate. Browser Sign out revokes only that browser session, including late-save resurrection protection. Explicit Sign out all devices, password changes and disablement revoke the account's sessions everywhere.
- Native auth uses opaque tokens, ten-minute access, thirty-day refresh inactivity and ninety-day absolute family lifetime by default. Refresh rotates under a database transaction, retaining the family ID and at most one valid refresh token. Known spent-token reuse revokes the family. Lost/uncertain rotation or uncertain secure persistence requires fresh sign-in, not blind refresh retries.
- Teddy serializes actual secure-storage operations and refreshes, verifies writes, and records a pending marker before rotation. Use `flutter_secure_storage` 10.3.4 and OS-backed storage; never store tokens in preference obfuscation. Keychain denial has a retry-access UI with actionable errors. An offline cached identity authorizes only owned local playback, never server calls.
- An expired server session must not automatically discard downloads or force away from usable offline content. Explicit Sign Out is different and intentionally destructive to that profile's device data.

### Two separate Stremio roles

One administrator-managed Stremio account supplies installed add-ons for the whole TED instance. It is never enrolled in watch-history synchronization by that role. Each TED user may separately link one Stremio account for two-way watch flags; its add-ons are not queried. Provider replacement/disconnection fences old handles/preparation without deleting favorites, recents or mappings.

The owner authorized migration of `native_tester`'s previously configured external account into both separate roles. Reusing the same external account is allowed; do not generalize the migration into an ongoing coupling. Admin add-on controls follow the personal Stremio history section in Your account. Disabled add-ons are server-wide, matched by manifest ID and public variant name so Torrentio and Torrentio RD remain distinct. Disablement fences new lookups, selections and renewal, preserving preferences; an already-valid playing link may finish.

### Joint accounts

Joint accounts are ordinary TED accounts with their own login, watch state and optional Stremio history link. In Your account → Joint account settings, an account can add existing users as members. A member may join multiple joint accounts but cannot itself host members. An account with members cannot join another account; no nested memberships.

A member sees read-only watch projections from its joint accounts. A joint account does not inherit members' viewing into its own watch state; the separate other-user indicators below follow the same display policy for every authenticated viewer. Neither direction grants authentication or mutation authority. Members may leave and choose the color of each joint-account indicator; a color is assigned initially. The joint account may remove members. No viewing or watch edits propagate through membership. Sharing an external Stremio history account remains allowed with an explicit warning that Stremio can indirectly synchronize their flags.

Poster badges stack vertically at the **top right**: the active account is white, joint accounts use assigned colors. Partial completion displays **watched / eligible total** in horizontally expanding pills for every account; fully completed scopes use a checkmark. Do not show remaining-count circles or clip multi-digit values. History/partial progress and current completion are distinct.

Teddy's Appearance → Poster indicator size controls all artwork overlays together (own/joint/other-user viewing, Watch List, ratings and overflow). Default 200% is twice the original badge size; offer 75–300%, independently of UI scale and included in portable settings/reset. Account columns may still shrink to the readable minimum before +X truncation on short artwork. Icons must scale and stay centered inside small rating overlays. Direct joint badges display that joint's own whole-title rating when it has retained viewing, without pooling member ratings.

Other-user indicators are visible to authenticated users of the same TED server. Show one avatar (initial when absent/unavailable) for each enabled non-joint account with retained viewing. Joint hosts never appear as other-user avatars; the viewer’s existing direct joint badges remain. For each other person, project their own viewing together with their enabled joint accounts, excluding any joint already represented for the viewer: the viewer’s own account or a joint membership shared with that person. Union by media identity before counting, so duplicate completions count once. A current completion from any included source wins over partial progress; otherwise display the greatest valid partial progress. Series/season ratios count distinct released eligible episodes, preserving the existing specials/main-season rules. Show partial episode counts or movie/episode progress beside the avatar; completed items retain the avatar instead of replacing it with a checkmark. Labels identify the account. Stack person indicators vertically below the active/direct-joint badges. Shrink them together only to a readable minimum; if the artwork still cannot contain everyone, reserve the final visible row for a “+X” badge counting the hidden people. Never render a partial badge beyond the artwork. These are read-only display projections: no credentials, raw history, edit authority, personal resume, Home rows, Person Watched classification or browse Watched-filter membership is inherited from another person. Account disablement and changed memberships take effect on fresh reads.

In web Movies/Series, enabling Watched reveals a separate row of account toggles: the active account uses white when enabled and joint accounts use their assigned colors. All start selected. Results are the union of retained viewing from selected accounts, including joint-only titles, filtered before pagination and combined with search, genre and Mapped. Deselecting an account removes only titles not viewed by any other selected account; selecting none yields no results. Membership is checked by the server, and this read-only filter never merges or changes watch state.

### Teddy profiles

Show a centered **Who's watching?** page on every app launch, even with one profile. Use circular profiles with names underneath and a matching circle containing a plus for Add profile. After selection, open the configured Startup Section. Authentication, synchronization and downloads wait for selection.

The account menu has Switch Profile, Change server and Sign Out. Add profile signs into another account with the server address read-only; Change server allows editing the origin. Preserve profiles for previously used servers. Keep one active authenticated context. Switching profiles/servers pauses and fences transfers, preserves scoped copies/reports/caches and resumes switch-paused work when that profile returns. It does not keep old profiles downloading in the background.

Explicit Sign Out stops/fences work and removes that profile's credentials, owned downloads, cached data and pending reports. This is the approved exception to report retention. Preserve other profiles, server history and the installation device ID. Server-origin changes are separate scopes, not automatic offline-data migrations.

## Catalog, metadata and artwork

Cinemeta supplies discovery and initial metadata, stable kind/title/episode identities, IMDb scores and schedules. TED owns numbering and file mappings; paths, RD IDs, download URLs and provider descriptive differences must not alter identity. Browse/search alone creates no mappings, library membership or downloads.

Use TMDb for movie enrichment and TVDB for shows, seasons and episodes through small standard-library adapters and River retries. Resolve provider/external IDs and unique official episode tuples; differing names/dates do not block verified associations. Conflicting/ambiguous identities remain errors. Keep original audio language separate from translated descriptions. English media and character names are preferred consistently on detail and person pages; avoid paired original-language character captions.

Lazy cache policy:

- Enrich a title when opened; request season episode thumbnails on season access and episode details on episode access. Do not eagerly enrich every episode or every filmography credit.
- Search, Movies and Series navigation uses Cinemeta summaries/artwork. TV spotlight focus/dwell never requests title enrichment, metadata polling or provider artwork, even when a cached provider snapshot is attached. Open details to fetch/poll enrichment. Existing user-edited poster files remain usable through the cached-artwork path. Home and personal/detail metadata behavior remain separate. Reuse the shared bounded/cached artwork pipeline for Cinemeta spotlight backdrops and portrait fallbacks.
- Fetch biography/filmography only when opening a person. Keep cached data on failed refresh and use bounded retries for failures, distinguishing failure from successful empty data.
- Explicit force-refresh includes a show's already-requested children and preserves artwork overrides. No monthly metadata-expiry policy is approved; daily Cinemeta schedules are separate.
- A missing TVDB biography may use TMDb by verified IDs; only when neither ID is supplied may the approved best-effort name match fill biography. Record heuristic provenance, keep TVDB person/role/filmography identity and never silently merge conflicting people.

Person filmography classification uses TED's current/retained legitimate viewing independently of metadata fetches. Watched includes the active account and its authorized joint-account viewing projections, without merging their progress/completion into the active account. Started titles count once they qualify as viewing; one viewed episode is enough to place a show in Watched. Completed flags remain separate. TVDB character images may replace posters in filmography cards, with a media-poster fallback; TMDb person portraits are not character-specific images. Show character names and normal watch badges. Do not invent unavailable character artwork.

Administrator web details expose Edit artwork beside RD Mappings. If metadata is uncached, request it and wait before opening the editor. Show exactly one active **Primary (poster), Logo, Banner, Backdrop** per movie/show and one poster per season. Do not expose all provider alternatives as active cached slots; episode artwork and other types are outside editing scope. Selecting a slot offers on-demand TMDb/TVDB alternatives or Choose file. Preserve the previous usable file on validation/replacement failure and preserve overrides through refresh. Keep those action buttons the same height. Other descriptive overrides remain deferred.

Provider keys are managed under administrator Server providers, stored on TED, never returned to clients. Blank inputs preserve saved values; red Remove saved key buttons remove immediately. Environment keys may be imported once for continuity, never resurrect an explicitly removed credential. A saved/configured flag is not proof of provider validity.

## Sources and playback

### Source identity and RD

Prefer validated owned local files, then saved mapped RD sources; allow explicit add-on selection without replacing mappings. Never silently change release on failure. Add-ons are aggregators with this resolution order:

1. Explicit torrent `infoHash` and `fileIdx` when supplied.
2. Recognized Torrentio/Comet binge-group extraction, with file hints.
3. Direct HTTP(S) URL only when torrent identity is unavailable.

Do not extract hashes from private URLs. Resolve identified torrents through TED's RD adapter. When adding a torrent, select only the specific requested episode/movie file, without first selecting the whole pack. Reuse existing entries when they expose a valid ready link for that exact file; preserve original account selections and pinned file identity. Another episode may need its own TED-owned entry. Existing season entries that return a single RAR remain unchanged while TED prepares the exact file separately. Journal add attempts/ownership durably, preserve them through cancellation and credential rotation, and never duplicate an uncertain add or mutate selections in background preparation. RD preparation/queued/downloading/unavailable/infringement failures must remain meaningful. Do not silently fall back to the add-on URL when RD resolution of an identified release fails. Local torrent/P2P, archive extraction/streaming, external pages and HLS/DASH proxying are unsupported. DMM links are external links, not scraping/import discovery.

TED serves stable authenticated playback endpoints and pins release/file identity through ranges, reconnects and link renewal. Renew authorization for new requests after access expiry. Do not put refresh tokens or private provider links in media URLs. Direct play only: no transcoding, tone mapping, normalization/remuxing or quality ladder.

RD inventory is a shared durable cache: initial full fetch, minute checks of the latest ten torrents, expanding incremental scans after changes, and daily full reconciliation to catch changes invisible at the head. Preserve the last complete inventory on partial/failing/changing scans and fence credential changes. Mapping browse uses it; Refresh requests incremental rebuilding. Admin status shows counts, successful check/full rebuild times, progress/failures and incremental/full buttons. A green **RD Direct Play** badge indicates resolvable identity; rainbow **In RD Account** requires the selected file to be ready, not merely a cached torrent hash. Playback/download preparation revalidates.

### Favorites, Recent and source picker

Favorites/Recent are shared server preferences for **Stremio sources only**, separate from personal viewing and RD mappings. Match add-on plus binge group within a show; without a group, use add-on/name for that exact movie/episode. Recent retains the last **three** and uses original actual-viewing timestamps, including verified offline provenance. Preparation alone never records Recent. Recent uses binge-group matching across episodes just like Favorites, but is ephemeral.

Default source/category preference is Favorites, then Recent, then existing mapped/local and season-selection heuristics as applicable. The locally remembered last source for a season is distinct from server Recent/Favorites. Reload refetches the current episode in both streaming and download selection. Cache public picker results locally for **twelve hours** per server/account/device/media; cached handles never authorize playback or bypass current disablement/credential/release checks.

Partial add-on failures may retry in the background. Once usable choices exist, retries must preserve visible source names, selection and enabled Play/source-menu controls, without showing a blocking source-loading state. Opening the menu does not wait for background recovery or restart it. If handles renew while the menu is open, preserve the chosen release by validated source identity; labels alone never establish equivalence. Empty initial failures retain bounded recovery and explicit Retry/Reload.

The modal has All, Favorites, Recent and installed-add-on categories. All deduplicates and orders favorites, recents, then remaining sources with separators; favorites lead individual add-on results too. Favorites have a gold star, border and title; Recent has the Continue Watching history icon and light-cyan treatment, including dropdown labels. The modal is dark, cards lighter and focused cards lighter again. Do not flood All with failed-add-on messages; show relevant unsupported/unavailable feedback when that add-on is selected and an actionable failure when no usable source exists. Keyboard traversal includes every card/category correctly and reveals each next card, not every second item. Escape dismisses this modal only.

### Player behavior and startup

TV buttons and seekbar are about 50% larger; center previous/rewind/pause/forward/next and vertically align right-side controls. Use one seekbar style with a round focused thumb. Remove chapter-jump and aspect-ratio controls; preserve aspect ratio. Marker skip buttons must not overlap the seekbar. Hide the idle cursor over playback in fullscreen and windowed mode.

Android MPV hardware playback uses direct MediaCodec decoding and the native video plane, avoiding copy-back through the GPU for supported media. Honor the hardware-decoding setting before renderer initialization. The already bundled ExoPlayer is an explicit Android alternative in Video Playback settings; changes apply to the next video. ExoPlayer chooses its decoders automatically. UI scaling changes controls and layout while preserving full-resolution native video.

Keyboard left/right shows and operates the seekbar; left/right on it only seek, Down moves to pause. Show cumulative seek feedback. Audio/subtitle sheets support mouse and keyboard. Escape closes an active player menu first; otherwise always returns to the current episode/movie details. Restore usable keyboard focus after window reactivation. Mouse motion can reveal controls, but **must not assign keyboard focus or trigger scrolling** (this later rule supersedes hover-to-focus requests).

Buffering shows the media logo with pulsing scale/alpha, never fully transparent. Buffered ranges reflect actual retained ranges after backward seeks. Keyboard and pointer seek paths must not interpret transient EOF, failed ranges, resume initialization or buffering as completion. Keep the player buffer capacity separate from initial fill so playback need not wait for its maximum cache.

Previous/Next attempts the same RD strategy or a unique same-add-on/binge-group release for the target episode. If unavailable, open that episode's details for explicit selection. Navigate the underlying route to the target **before** source loading so Back returns to the right episode. Download playback only enables adjacent actions when an eligible downloaded target exists. Explicit Next marks the current episode watched at any offset using a distinct durable terminal report. Natural end only advances when autoplay is enabled; default off.

Audio: per-title/show local override → original language → English → file default. Subtitles are enabled: local override → English → file default. Cache original language with downloads and apply the same policy offline. Manual track language changes update local preferences, not server-global metadata.

Configure bundled subtitle fonts before opening MPV media, including plain embedded SubRip tracks without attached fonts. Before exposing a recoverable playback failure, reopen the same pinned session at the retained position up to three times per player session. Preserve pause intent, speed, track choices, source identity and immutable viewing reports; never infer completion from the interruption. Authorization/source-change/unsupported/placeholder failures keep their specific action. Transient session-preparation failures may retry the exact choice up to three times; leaving the route cancels further attempts. HTTP transport/auth failures in ExoPlayer must retain their status for TED recovery rather than trigger a format fallback.

Persistent playback diagnostics record session preparation attempts/failures, native errors, recovery starts/outcomes/exhaustion, media/session/backend/position context, subtitle availability/selection and font setup. Failures remain visible with ordinary logging enabled. Record safe codes/statuses and bounded technical fields; never persist provider URLs, headers, tokens or raw provider response bodies. Specific RD errors must not be flattened into “TED is unavailable.”

Accepted startup optimization: reuse bounded validated head/index bytes within prepared sessions, overlap independent reads, deliver the rest of the 2 MiB header progressively, and avoid waiting for the full player read-ahead cache. On details, prepare only the selected Favorite/Recent already-ready RD source, cancel obsolete work and never add torrents just by browsing. Play still authorizes a fresh session/resume snapshot. At 50% of an online episode, fetch the next episode once and prepare only a unique matching add-on/binge group; discard on navigation/selection change. Further tuning is **investigation only** until requested; see [the deferred startup review](docs/startup-and-account-review-2026-10-14.md).

## Viewing rules and synchronization

### Qualification, completion and history

Automatic live/offline viewing must not create history, Continue Watching, resume progress or viewing-based Recent before **at least 15% of a known runtime**. Current implementation qualifies by reported playback position, not a wall-clock stopwatch. Once a session qualifies, backward seeks keep its legitimate progress. Keep immutable ignored intake receipts for duplicate/stale replay protection. Unknown runtime does not qualify by position; genuine natural completion can qualify. Preserve existing saved history/progress; do not retroactively erase brief historical sessions.

The minimum-viewing threshold is separate from **90% completion**. Known-duration EOF below completion is interruption, not completion. Unknown runtime never means zero-length completion. Explicit watched edits and explicit player Next remain allowed at any offset. Actual file duration at or below **30% of known expected runtime** is a placeholder safeguard: no watch/Recent/completion/autoplay and no published download. Unknown runtime is not a short file. Preserve prior state on rejected viewing.

Keep current completion, resume, viewing dates, retained history, observations and outgoing acknowledgments separate. Unwatch preserves history. Confirmed Never watched clears that user's title-scoped history/completion/resume/count/date, including retained episodes outside today's catalog, while retaining replay protection. Do not change other users, files or membership. Watched now records an explicit date; Watched without a date does not invent one. Show/season mark actions apply to the scope, including specials where selected. Edits work without mappings/library membership.

Show counters use main-season episodes with known release dates on or before TED's configured day. Exclude specials and unknown/future episodes from show totals while retaining individual editability. Season cards use the corresponding eligible season scope. Partial playback alone does not increment completion. Refresh schedules daily and on details; preserve previous complete schedules on failure.

### Watch List and ratings

Watch lists and ratings belong solely to the signed-in account, including when that account is a joint host. Never merge these collections or Unrated with member/joint activity. Only whole movies and series can be saved or rated. Watch List follows Movies/Series in both navigation menus and offers Movies/Series only, default Movies. Teddy uses Search's summary-only TV spotlight/shelf and desktop grid with a type dropdown and no search bar. Adding, starting, finishing, marking watched or unwatching never automatically removes a saved title; only an explicit removal does. Poster bookmarks are green at top left, following the RD mapped badge in TED web.

The seven personal ratings are 1 Absolute trash (poop, dark red), 2 Very Bad (stacked double thumbs down, red), 3 Bad (thumb down, pale red), 4 Average (dash, gray), 5 Good (thumb up, pale green), 6 Very Good (stacked double thumbs up, green), 7 Absolute cinema (raised open palms, dark green). The two palms retain their individual proportions, leave vertical breathing room and have a visible horizontal gap. Movie/series details provide save/remove and a rating picker. Choosing, adjusting and clearing ratings are explicit actions. Ratings may include unviewed titles and never create viewing history. Own rating badges occupy the poster's bottom left.

Ratings has a horizontal shelf per category in Teddy and collapsible poster grids in TED, plus Unrated. Unrated contains this account's retained qualified started viewing, even a partial movie or one episode/special, deduplicated to whole titles and ordered by most recent viewing before pagination. A historical view remains eligible after ordinary unwatch; Never watched still removes that history at its existing boundary.

Returning from details to Ratings refreshes category membership while preserving focus in the departed row and its viewport position. Adding/removing a shelf above that row must not reveal another category or realign the returning shelf.

Other-user watched-avatar badges carry a small rating at top right only for people with retained viewing. Use that person's own rating first; otherwise use the maximum rating among enabled joint accounts included in that person's watch projection, with the same exclusion of joints already represented for the viewer. This is a read-only projection; personal collections are never merged or changed, and ratings on unseen titles never expose that person to another account.

Other-account rating circles (including direct joints) use about 69% of the watched-badge diameter, 50% larger than the original 46% size. Offset them slightly beyond the top-right corner to keep the initial/avatar centre visible while remaining inside the artwork.

### Home rows

- **Continue Watching:** qualified unfinished movies/episodes, including specials and rewatches. At most one per movie/show, selected before pagination by most recent unfinished episode viewing timestamp; numeric season/episode breaks ties. Preserve other progress/history.
- **Next Up:** for shows absent from Continue Watching, anchor at the most recently completed or marked-watched regular episode, breaking timestamp ties numerically. Choose the first released **unwatched** regular successor, skipping watched successors and leaving earlier gaps alone. Never backfill earliest gaps, pick merely the furthest ever watched as the anchor, invent episodes or clear flags. Known action/completion dates outrank initial import receipt fallback. A fully watched show has no candidate until an eligible successor is unwatched.
- **New Releases:** immediately below Continue Watching when nonempty, a subset of Next Up whose candidate release day is strictly later than the anchor watch day. For undated viewing, compare to **TED account creation**, not import/title-addition time. This fallback does not fabricate dates or change the Next Up anchor.
- **From your Watch List:** immediately after Next Up when nonempty; random order sampled afresh from the entire account list on each Home entry. Cached entries are also reshuffled immediately.
- Popular Movies and Popular Series come from Cinemeta. Poster captions show the available IMDb score in gold before the year; do not substitute provider community scores.

Teddy persistently caches all six rows per server/account/device. Show cached data immediately while fetching rows independently; replace cache/UI on complete success, including successful empty results. Retain usable snapshots through failure/partial data, and fence responses from switched profiles or older loads. No per-row “Loading …” text. A first launch with no cached content may show one progress indicator. Sign Out purges only that profile's cache. Cache is display data, never authorization or a source of watch mutations.

### Stremio history

Personal links automatically synchronize the entire account, polling each distinct external account every 60 seconds by default. No selective title enrollment or per-user exclusions. Persist snapshots and serialize account writes across restarts. Import watched/unwatched flags and optional validated completed-episode recency, not resume positions, fabricated play counts or viewing dates inferred from receipt time/playback timestamps alone.

Outgoing writes are flag-only: reread immediately, merge without changing unrelated fields, persist intent, write and verify read-back. Stremio offers no atomic conditional write; the owner accepts the remaining concurrent-client overwrite window. Recognize write echoes, preserve unknown outcomes for recovery and never turn a failed/partial read into unwatched state. Missing titles can be created only after confirmed absence, as temporary removed history entries without pinning library membership; absent unwatch is a no-op.

The administrator's temporary account-copy tool is separately restored: additive whole-account copy from a read-only source into a distinct destination, preserving destination flags/fields and verifying writes. It neither links regular sync nor creates TED viewing history. Retain its journal/backups until explicitly retired; do not reset the destination during cleanup.

## Native UI and navigation

Sidebar order is Search, Home, Movies, Series, Watch List, Ratings, Downloads. Default destination is Home after profile choice. Bottom controls are Reload, Settings, Account with display name last; no add-on button or account-menu Settings. The account picture/initial must remain circular and centered when collapsed, expanded and during the transition; constrained avatar space must never stretch its clip into an oval. Page changes require Enter/click, not focus. Keyboard activation immediately enters the selected page: Home starts at the first card of its first populated row, Search at its input, Movies/Series at the first media card, Watch List at its type filter, Ratings at its first populated shelf, Downloads at the first entry (toolbar if empty), and Settings at its controls. Reselecting the active destination also enters its starting control. If content must load first, transfer focus when ready only while the activation still owns focus; later navigation/pointer use cancels that transfer. Right-arrow entry separately preserves the remembered position. Mouse hover uses a pointer cursor. Collapse to icons when neither hovered nor keyboard-selected unless Keep sidebar open is enabled; collapsed width is equal in TV/desktop. Active page foreground and keyboard white-outline focus are independent.

No redundant top-left Home/Movies/Series/Search headings. Keep sidebar visible on details and Settings. Media/person pages have a fixed top-left Back immediately after the sidebar. Episode Escape always follows episode → season → series → original caller even when entered from Home. Settings/license screens return through their caller; active dialogs consume Escape first.

Entering movie/episode details, including return from playback or a person page, focuses Play/Resume as soon as it is usable. Retain that pending focus while source readiness or the returning playback action is still loading. Ordinary sidebar traversal within the same details page retains its remembered lane.

Horizontal lists remember the last section when returning from sidebar. Right from sidebar enters media, Up from media accesses filters. Up never transfers page-content focus into the sidebar: at the top of Search, catalog filters, Home banner or Downloads controls it stays in the page. Up/Down within the sidebar still navigate its own items. Holding Left stops at the first item; a fresh Left press exits to sidebar. On Home, Up on the first visible row does nothing. Keyboard focus on whichever Home row is first reveals the top banner, bounded to keep that row visible at the bottom with small padding. Do not special-case Continue Watching. Pointer hover never focuses, locks a row or scrolls; wheel/click/drag remain normal. Deferred focus-reveal must recheck input mode.

### Pages

- **Home:** top-to-bottom darkened header gradient; no diagonal gradient. Banner carousel is entirely noninteractive in TV mode. Shared media-row card sizing and tighter global row spacing; no View all buttons. Preserve shared card inset behavior rather than globally stripping Plezy padding to alter one page.
- **Movies/Shows:** desktop grid with Home-style poster proportions. TV has a single horizontal paginating row, filters above it and a static selection spotlight above that, without carousel/play/details buttons. Align summary, filters and posters. Filters: Mapped, All/Watched/Fully Watched/Partially Watched, genre, Popular/New. Partially watched movies are in progress; shows include started shows with incomplete regular episodes. Watched can include legitimate retained viewing; fully watched is current completion.
- **Search:** same desktop/TV media presentation, only Type (Movies/Series, left) and query. No Mapped, watch, genre or sort controls. The separate Mapped-items page is removed.
- **Movie details:** selection-style banner with release date; left action row Play/resume-progress, Start from beginning, Download, Toggle watched. Right selectors choose RD mapping or Stremio and the selected file/source. Default RD when available; Stremio-only has a noninteractive type selector. RD filename is noninteractive; Stremio opens the picker. Load sources on entry; disable Play until usable. Keyboard focus on any action-row control reveals the top; visible focus/hover treatment for Play.
- **Show details:** shorter banner, release date, Download show and Mark show watched/unwatched. No Play/restart. First focus is seasons, not Download. One horizontal season row before cast, with Specials last and watched/total badges. No separate Up Next block. Focus reveals the whole season row rather than clipping its bottom to expose the entire hero.
- **Season:** shared show banner with a larger season poster and centered show logo, season release date, Download season and Mark season watched/unwatched, then episode thumbnails. Episode name is prominent; Season X Episode Y smaller below, followed by release date. Keyboard/remote episode focus reveals as much hero as fits while keeping the entire episode row and captions visible with a small bottom margin, independently of UI scale. Deferred reveal rechecks input mode and current focus.
- **Episode:** larger episode thumbnail at left, show logo, episode description/release date, movie-style playback/source actions. Season and episode cast rows reuse the show's cast; do not make extra per-episode cast lookups.
- **Cast/person:** cast uses portrait proportions and a leading section icon. Person hero uses the originating title's backdrop, portrait/basic data left and biography right with close spacing and vertical centering. Truncate long biography with keyboard-selectable Read more dialog. Watched then Other horizontal filmography rows have section icons; hide empty Watched. Keyboard focus on the first actual row reveals the hero; re-reveal focused content if late Watched data inserts above Other. Hero may cut off part of the second row; fitting both is no longer a requirement.

## Downloads

Device downloads and server-managed local copies are separate. Preserve original media container, stable identity, exact source provenance and verified ranges. Validate before atomic publication; never publish provider placeholder videos. Original language and episode thumbnails are cached for offline use. Partial checkpoints are resumable only against the same validated release; cancellation/removal/migration coordinates active playback and transfer.

Teddy Downloads is a sidebar page with alphabetically mixed movies/shows. Show rows expand to indented season rows using season posters, then more-indented episodes; movies use a nonexpandable equivalent and play the owned local version on selection. Show/season disk size aggregates downloaded children. Trash deletes only owned files for that scope. Left from show/season moves to sidebar rather than collapsing.

Progress: shows/seasons display Downloading X of Y; movie/episode rows show progress and speed, and details Download actions show active progress. Show/season watched totals count **downloaded episodes only**. Downloaded movie/episode completion counts only viewing after that copy's download timestamp, even if another source was watched. In-progress state is always considered once legitimate, regardless of download time. This does not reset normal server watch state.

### Bulk source planning and recovery

Download show/season first asks All or Unwatched and constructs an ordered episode queue. Show a source picker (including available RD mapping) for the first eligible episode. Carry the explicitly selected RD strategy or unique add-on/binge group forward until a matching source is unavailable; then ask for that episode's source and continue using the new choice.

Cancel aborts the entire staged operation. Skip current advances; Skip remaining commits only already-selected entries. No downloads start before the reviewed plan is atomically committed. Each entry retains its exact selected release independently of expiring source handles.

Failed individual/show/season items support Retry and Change selected source. Retry retries only failed entries; changing a show/season's failed sources runs the same planning/matching flow for those failures only. Validate/journal replacement before resetting owned failed bytes. Preserve completed/active copies, watch state, pending reports and restart recovery.

Download range size is configurable, default **512 MiB** (8–2048 MiB supported). Max simultaneous downloads defaults **1**, range **1–10**, shared across individual/bulk/retry work. Increasing admits waiting copies; decreasing drains active ones without interruption. Paused/removing/migrating/shutting-down work stays fenced. A stale queue snapshot must not re-admit a just-finished job.

### Settings

One vertically scrolling page, Plezy-style grouped tiles, section headings and separators, no category submenus. The following are the **approved defaults**; implementation exceptions are listed in IMPLEMENTATION rather than silently changing this contract.

| Section | Options/defaults |
| --- | --- |
| General | Startup destination from the shared sidebar inventory: Home; Force TV mode: off, layout restart; Start fullscreen: off |
| Appearance | UI scale: 100%, manually selectable 50–150%, applied immediately and persisted; Poster indicator size: 200%, selectable 75–300%; Theme: system; Hide spoilers: off (blur unwatched/in-progress episode thumbnail and description when enabled); Watched indicators: on; Keep sidebar open: off |
| Video Playback | Hardware decoding: on; small skip 10 s; large skip 30 s; rewind on resume 0 s; autoplay next: off; intro and credits modes Off/Button/Automatic with off default; chapter markers: on; click video to pause: off; exit fullscreen on close: off |
| Downloads | App-data storage by platform; changing root moves existing owned files; Wi-Fi only: off; auto-remove watched: off; range size 512 MiB; simultaneous downloads 1 |
| Advanced | Reveal logs; clear local image cache with confirmation; reset settings with confirmation |
| Backup | Export/import portable settings, excluding credentials/device paths |
| About | App/version and open-source licenses |

Retain Plezy marker regex defaults: intro `(?:^|\b)(?:intro(?:duction)?|opening)(?:\b|$)|^op(?:\s?\d+)?$`; credits `(?:^|\b)(?:outro|closing|credits?|ending)(?:\b|$)|^ed(?:\s?\d+)?$`. Settings import/reset/cache clearing must preserve credentials, installation ID, history, downloads and pending reports. Download relocation uses a durable verified-copy journal, fences transfers/playback and only activates a validated root; portable imports do not relocate files.

## Web management and data lifecycle

Responsive desktop/mobile management remains required. Movies/Series discovery has **Mapped** (renamed from Saved) and Watched filters with the account toggles described under Joint accounts. Filtering uses complete selected-account/mapped collections rather than only the current catalog page; intersections/search are retained. Web discovery may offer Featured and year-based New browsing in addition to the native Popular/New controls. Source-first RD mapping supports manual episode tuples and guided season ranges; Specials remain individually manageable.

Mappings validate before atomic activation, preserve old sources on failure, use sequential **200-assignment** batches by default and **four** measured validation workers. Completed batches survive later failure; retry resumes unfinished work. Explicit remapping may interrupt playback and change cut/resume offsets, accepted by the owner. Native activation no longer awaits external publication acknowledgment. Delete/re-add uses stable IDs, tombstones and fencing; catalog refresh cannot resurrect explicit removals. Never guess torrent/title associations.

Server-local download jobs persist transfer/validation/publication phases in River/PostgreSQL, use same-filesystem staging and only advertise complete validated copies. Shared-media destructive operations remain administrator-only. File removal deletes only owned media and preserves personal history. Daily cleanup only removes unreferenced owned files, not mapped downloads or owner backups. Metadata recovery archives do not imply an active restore UI.

## Persistence, operational invariants and deferred work

All durable server state is in explicit host files: Config/postgres, Config/metadata, configured media and preserved recovery archives. No named/anonymous volume, container layer or Colima disk may be the only durable copy. Preserve credentials, source descriptors, history, jobs and replay protection through upgrade. Native Drift retains exact report IDs/sequences/timestamps/account-device scope until validated receipts; only explicit profile Sign Out authorizes purging pending reports. An ordinary local-copy deletion does not.

Use bounded external requests, validated HTTP ranges, cancellation, credential fencing, scoped link caches and meaningful redacted failures. Failed reads are not successful absence. Optimize only from measurements; never weaken file identity, authorization, durable jobs, locking or recovery for speed. Logs/status remain distinct from successful operations. Migrations are forward-only and must not silently repair malformed persistent state by deleting it.

Deferred: More like this recommendations; additional debrid providers/Prowlarr; archive-member RD streaming (Reborn investigation only); monthly metadata expiry; watchlists; general descriptive overrides beyond artwork; app stores; React/Capacitor; automatic shared-media eviction; persistent cross-session startup-byte cache; further measured startup optimizations; broad non-Mac T6 and release/restore hardening. Existing optional device auto-remove-watched is not automatic server eviction. Personal NOTES are not approval for new features.

### First-run administrator setup

An installation with no accounts shows a web setup page to create its first local administrator using the configured password policy. Creation is serialized in PostgreSQL and refuses once any account exists, including disabled accounts. The user then signs in normally. CLI bootstrap remains an optional alternative. Database failures must never be treated as an empty installation.

Android/Google TV automatically enables TV controls from native UI mode or device features; Force TV Mode is only an override. Android Back follows Escape navigation, dismissing active text input or a dialog before leaving the screen. Add Profile returns to the profile chooser rather than exiting the app.

### Release updates and application identity

Teddy’s Look for Updates section offers Stable and Release Candidate channels and user-triggered download/install of newer compatible releases. RC includes stable; do not silently downgrade or cross TED/Teddy’s shared major boundary. Close playback, downloads and storage before desktop replacement, preserve user data, verify downloads and retain a rollback bundle. Android uses the system installer and permission flow.

Only TED administrators see a bottom-center new-version notice. Dismissing snoozes all version notices for that admin for one day across browsers; no permanent opt-out or server-update button. Discover actually published images, not reserved release tags. Operators remain responsible for their deployment.

Use each project’s supplied icon.png as its application identity: TED web favicon and Teddy native launcher/splash icons, wide Android TV banners, notification masks, Windows tiles/taskbar variants and Apple icon resources. Generate wide/transparent variants from that master without distorting its mark; do not ship inherited Plezy application graphics. Keep required upstream attribution and independent provider/player logos.
