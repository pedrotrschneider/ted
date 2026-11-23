# Next-episode preparation and future seek previews

Owner decisions from November 19–20, 2026, with the November 23 mapped-Next failure clarification. This records the discussion so work can continue without the chat. Requirements below are approved; implementation and verified evidence belong in IMPLEMENTATION.md. Do not interpret this plan as proof of device behavior.

## Report and investigation

The owner uses MPV on Chromecast with Google TV, with Teddy 0.3.11 at the time of the report. They verified near-instant Next on Mac, but Chromecast Next takes approximately as long as a fresh start and initially shows no buffered region. When continuation matching fails, target details sometimes still waits for sources.

The existing implementation triggers once after 50%, only for add-on sessions with a binge group. It requires a unique same-add-on/group choice whose exact RD file is already ready; it cannot add the missing file or retry. Preparation errors are swallowed, so selection can appear successful without warming. Picker results are cached for twelve hours, separately from authorization/readiness; the trigger can skip fetching them, and cached partial results can suppress recovery. Server warming retains bounded header/index bytes rather than a playable five-second window. Each player route recreates MPV; there is no next-file device cache. The latest RD-default details changes also introduce a continuation selection/cancellation race. Stable TED IDs can differ from provider display ordering, so parsed season/episode arithmetic must not reject the server's actual successor.

Prior audit evidence is in `/private/tmp/ted-next-audit-20261119-*`; those files are evidence, not required documentation. Automated tests and Mac owner observations do not establish Chromecast behavior.

## Approved next-episode behavior

At 50% of an online episode, prepare one successor from the authoritative episode targets, including when playback starts beyond halfway. Preparation is independent of the autoplay setting. It never creates viewing, completion, Resume, Recent or watched-sync events.

Expose **Next Episode Matching Strategy** in Video Playback:

| Choice | Policy |
| --- | --- |
| Manual | Fetch/cache the next episode's sources only. Next always opens its details for explicit selection. |
| Binge Group (default) | Current mapped playback can use only the next mapping. Current add-on playback requires a unique same-add-on/binge-group match. |
| Best Guess | Opt in to additional same-add-on signals after the stricter matches. Require a clear unique winner; ambiguity opens details. |

If the current source is RD Mapped and the next episode is not mapped, skip media warming/device caching and open target details. Never silently switch that case to an add-on. Local/offline adjacency retains its existing verified-download policy.

For add-on Best Guess, prefer a unique binge group, then verified same-pack/exact-next-file identity, then normalized release filename, then technical similarity. Normalize recognized season/episode forms such as S01E02, 1x02 and Season 1 Episode 2 without erasing release group, cut, year, language or technical distinctions. Technical signals include resolution, source type, video codec, HDR/Dolby Vision, audio format/channels/language and approximate bitrate. Bitrate is a soft similarity signal. Candidates must come from the explicit next episode's lookup; never invent media identity from names. Once chosen, pin the release: a preparation failure must not silently choose another.

Persist source lists using the same twelve-hour scoped cache as details, including when matching fails or Manual is selected. A retry for no match must request fresh provider results rather than repeatedly reading the same cached list. A usable partial list remains usable while failed add-ons retry; stale cached badges never establish current RD readiness or authorization.

## Server preparation and retry

For an identified match, TED must reuse the ready exact file or durably request that specific file in RD. Preserve existing RD selections/originals; never select the entire pack first. Existing durable jobs/journals, locks and credential fencing own external effects and restart recovery.

The stages are lookup → match → request exact RD file if necessary → wait for readiness → validate/pin file → warm server bytes → cache on device → ready. No-match/transient lookup/server-preparation failures retry every minute, with one attempt in flight. Respect longer provider retry delays. Permanent authorization, disabled-provider, unsupported or changed-source failures wait for a meaningful state change instead of an endless destructive retry. Manual successfully fetching a list and an unmapped successor under mapped playback are deliberate details outcomes, not reasons to start media effects.

After server preparation succeeds, start device caching immediately. Verification of the mapping/exact selected source and its prepared session establishes continuation readiness. Packet-window planning belongs to device caching: a failed, unsupported or pending cache plan must not veto playback of an already verified successor. Next authorizes its fresh viewing session even while that optimization retries. A device-cache failure retries every minute independently; retain verified partial bytes for the same pinned representation and avoid repeating completed RD adds. Known authorization, file-change and placeholder failures still invalidate continuation. Expired preparation or changed file identity requires revalidation. Cancel obsolete work on navigation, source change, account/profile change or shutdown. Late completion cannot publish into a successor context or unexpectedly start playback on details.

## Device caching and player reuse

Target five seconds ahead of the intended next resume position, bounded by that device's current forward buffer budget. Reaching the byte cap counts as bounded readiness even when five seconds does not fit; do not retry forever. Fetch required container headers/index information and verified playable ranges. Fill forward first, then the backward window around the intended position, capped by the current backward budget. At position zero no backward bytes exist. Recheck the intended resume position when Play/Next authorizes the fresh session.

Container initialization can include attachments/indexes far beyond a small fixed suffix. The packet planner supplies bounded observed initialization ranges alongside the playable window. Retain their union under the same forward budget, counting overlaps once, before filling forward and backward media. A cache of video packets alone does not establish that MPV can open without further network reads. Older servers without these optional ranges retain the bounded header/suffix fallback; both updated components are needed for complete measured initialization coverage.

Use the same budget ceilings as current playback; active playback has priority and speculative bytes are trimmed/evicted under pressure. Android's existing fixed tiers use `ActivityManager.largeMemoryClass`: <=256 MiB → 32/16 MiB forward/back; 257–512 → 64/32; >512 → 100/48. The owner's Chromecast reports a 384 MiB class, yielding 64+32 MiB ceilings. This class is the firmware's large Java/Kotlin heap allowance, a proxy for budgeting; it is neither total device RAM nor an MPV/native-memory limit. Changing those tiers is outside this increment.

Playback must actually consume the retained verified bytes. A background fetch alone is not readiness. Prefer extending the existing authenticated loopback transport with a bounded speculative memory cache, without a second decoder, disk-cache resurrection or new dependency. Preserve fresh authentication, exact ranges, representation/file pins and profile scope. Container indexing and decoder initialization may still impose latency; measure actual first frame on Chromecast before promising instant playback.

Reuse the MPV engine across episodes; recreating the decoder for differing codec/video formats is expected. Retain the idle engine for 30 seconds on next-episode details when continuation cannot play, so explicit source selection can reuse it. A new episode still needs its own authorized session, tracks, resume snapshot, first-frame state and durable viewing reports. Fence old events and teardown/display callbacks; expiration, profile changes and shutdown release resources safely.

## Seeking decisions and future nice-to-have

TV seek confirmation is implemented separately in Teddy commit `23098b535c674d04214ff351d0d315f46ce009f3`: Left/Right previews the cursor/time, holds accelerate, Enter/Select commits one native seek, and Back/focus loss/hidden seekbar/five-second inactivity cancels, including while paused. Mouse timeline behavior is preserved. TV means automatic form-factor detection OR Force TV mode; the setting does not disable positive automatic detection. No push or app build was requested for this commit. Physical Chromecast acceptance remains pending.

**Seek thumbnails remain a future nice-to-have**, explicitly retained by the owner on November 20. They are not part of the current next-episode increment. The desired interaction is a YouTube-style thumbnail at the pending seek target. Buffered bytes may help, while uncached positions require a separately designed bounded extraction/fetch path. No implementation or dependency is chosen. Future investigation should examine provider storyboards, existing shared thumbnail hooks, local frame extraction, cache coverage, cancellation, memory/network cost and MPV contention. Do not assume a raw byte cache contains independently decodable frames or that a thumbnail requires fetching only one frame. Preserve the seek-confirmation/cancellation behavior when adding previews.

## Verification and delivery boundaries

Cover retry timing and single-flight/cancellation, failed preparation, exact-file RD recovery, stale readiness/cache results, matching ambiguity and false positives, target-details defaults, provider display ordering, fresh resume, range/representation changes, cache reuse/budgets/eviction, engine reuse/event fencing/30-second expiration, and immutable viewing boundaries. Use isolated persistence and synthetic HTTP fixtures without mutating owner history. Instrument safe phase durations and buffer reuse; never log private URLs or credentials.

Chromecast MPV acceptance is required for first-frame latency, visible buffered ranges, memory pressure, mixed formats, seeking and resource/display retirement. Mac success is separate evidence. The owner subsequently authorized committing and pushing both repositories to GitHub to trigger Actions, including the earlier seeking commit; that supersedes the initial no-push instruction. GUI operation and local Colima restart remain unauthorized. No new library is approved.
