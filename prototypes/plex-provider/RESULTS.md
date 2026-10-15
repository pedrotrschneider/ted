# Observed results — October 10, 2026

**Historical, unselected experiment.** TED now uses its own native Teddy client. This prototype is not deployed, not a runtime dependency and not a proposed next implementation stage. Commands/results below describe the isolated October10 experiment only; do not start its old services as part of TED operation. Preserve any owner runtime data and required notices. Current direction: [PROJECT](../../PROJECT.md).

**The backend mechanism is promising, but this experiment does not establish a viable Stremio-style experience in stock Plex clients.** The remaining problems occur between provider registration, client discovery and native queue creation. A successful provider registration or transcoder response is insufficient to justify porting TED.

Environment: macOS Apple Silicon, repository Devbox tools, existing Colima environment, isolated official Plex Linux ARM64 container. Plex Media Server **1.43.4.10903-e5521bd8c**, embedded Plex Web **4.160.0**, documented API specification **1.2.3**. The Compose file pins the actual Plex image digest. Tested prototype source hashes are in `evidence/tested-sources.json`; this experiment is uncommitted and does not claim a repository commit as its tested revision.

## Evidence

| Boundary | Result | Evidence |
| --- | --- | --- |
| Register an external provider | HTTP 200; provider ID 2 | `registration.json` |
| Preserve registration and fixture state across container restart | Stable server identity, provider ID, fixture GUID and 21-second resume state | `restart.json` |
| Browse through Plex's reverse proxy | Two provider directories; 50 movie and 50 show entries | `provider-sections.json`, `movies-proxied.json`, `shows-proxied.json` |
| Cinemeta search through the Plex proxy | Batman returns movie/show result hubs; no ordinary library insertion needed | `search-proxied.json` |
| Movie, episode and series metadata | Generated movie/episode details and series children return successfully | `fixture-proxied.json`, `episode-proxied.json`, `series-children-proxied.json` |
| Ordinary Plex database materialization | 0 `metadata_items`, 0 `library_sections`, 1 provider resource after these browsing tests | `database-counts.json` |
| Native playback decision | Plex reads the remote XML metadata and reports “Direct play OK.” | `native-playback-decision.json` |
| Native HLS transcoding | Master/variant playlists and three video segments return 200; FFmpeg decodes five seconds of the first segment | `transcode.json`, `verification.json` |
| Provider-specific queue | One generated fixture returned | `provider-playqueue.json` |
| Normal owner-authenticated provider discovery | Returns only `com.plexapp.plugins.library`; TED absent, including tested query variants | `discovery-owner.json`, `discovery-owner-included.json` |
| Native queue used by Plex Web | HTTP 200 with **zero items**, including the exact URI seen in the owner's Play request | `native-client-playqueue.json` |
| Binary range delivery through the provider proxy | Strict HTTP client rejects duplicate Content-Length; direct provider ranges work | `stream-range.json`, `stream-range-curl.headers`, local range regression |
| Native Plex Web detail/playback | Owner reports title appears, playback fails after XML correction | `owner-check.json` |
| Synthetic watched/resume delivery | Explicit CLI timeline request reaches provider; resume and Continue Watching survive restart | `timeline-write.json`, `resume-read.json`, `continue-watching.json`, `restart.json` |
| Local behavior and race tests | Four tests pass in standalone prototype module | `verification.json` |

The zero database counts apply only to these specific provider-browsing tests. They do not establish every Plex client's behavior or the behavior of ordinary libraries.

## Corrections made during the experiment

Plex appends reverse-proxy paths to the registered base URL, so the provider must support `/provider/library/...`. Its proxy also forwarded chunked headers with decoded JSON/XML bodies; explicit response lengths resolved that framing failure. Finally, Plex's internal XML consumer requires `<Video>` and `<Directory>` elements rather than JSON's `Metadata` element name. Correcting XML changed the owner-visible result from an error screen to a rendered title and made native media decisions succeed.

The remaining range-delivery failure is separate. A range request for bytes 1000–1999 produced a response with an incorrect full-file Content-Length and an additional length, rejected by Node's HTTP parser. Forced HLS transcoding works because Plex reads the internal source URL itself and serves native HLS segments. It does not make Plex Web's empty native queue work.

The owner's actual Play request uses `/playQueues` with a normal library-provider URI containing the custom proxy metadata path. Replaying that request returns an empty queue. The provider's own queue endpoint returns a fixture, but normal discovery does not advertise this provider, and the observed Web path does not use that endpoint. This is a concrete client/server compatibility gap. We have not established whether it is a configurable server restriction, missing support for arbitrary external providers, or additional undocumented requirements. We did not modify Plex's database, patch its client, impersonate its built-in provider, or intercept normal library API routes to manufacture compatibility.

Older files such as `proxy-framing.headers`, `plex-playqueue.json` and the first registration captures preserve intermediate failures. Use the current `summary.json` and the table above for the final state. The prototype diagnostic UI is only a provider inspection page, so its playback is not listed as a Plex Web success.

## Implications for TED

Go/PostgreSQL catalog identity, source mappings, persistent jobs, downloads, and the Stremio adapter are largely independent of the media-server UI and could remain. A production adapter would need separate publication, permission, metadata recovery, playback-source and watch-event contracts. This prototype neither ports those contracts nor verifies production Real-Debrid playback, seeking/link renewal, per-user watch reconciliation or metadata recovery.

The experiment does show that a remote catalog need not be materialized as normal Plex database items for the tested API reads, and that Plex's transcoder can consume a remote HTTP source. However, **the two dedicated Cinemeta libraries are not available in normal Plex navigation, and stock Web playback fails**. I would resolve those two issues in stock clients before investing in a TED port. There is no evidence here that changing media servers alone delivers the requested feature.

Other clients, remote access, mobile/TV playback, actual Web-generated history, and full-catalog pagination are unverified. The experiment caps lists/search results and maps no Cinemeta movie to a source. It uses only the generated video and prototype-owned state.
