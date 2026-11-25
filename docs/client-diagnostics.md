# Teddy diagnostics in TED

Teddy sends redacted application logs to its active TED server by default. Administrators open **Logs** in TED's web navigation to query them, download a bounded NDJSON page and change retention. Members can upload their own device logs but cannot read any logs, device inventory or diagnostic settings. Both browser and native API routes enforce this on the server.

In Teddy, **Settings → Advanced → Send diagnostic logs** disables uploads and removes queued diagnostic batches from that installation. Local diagnostic files remain available through Reveal logs. This setting is included in portable settings/reset; it does not change authentication, viewing reports, downloads or server history. Disabling cannot retract batches TED already accepted. **Detailed diagnostic logging** is off by default; enabling it also enables bounded startup/transport profiling for subsequently opened playback. Ordinary failures and actionable phases remain visible without it.

## Retention and storage

Retention is an administrator setting in TED, default **90 days**, selectable from **1 to 3,650 days**. It uses TED receipt time rather than the device clock. Lowering it deletes older diagnostics in the same transaction; the web UI requests confirmation before applying a reduction. Increasing it cannot restore deleted data. River runs hourly cleanup, including on startup, and ingestion also prunes expired data. Queries exclude expired data even before cleanup runs.

Migration **50** stores compressed batches, sanitized searchable message text, metadata and retry receipts in the existing PostgreSQL database. There is no additional container, directory, service or library. Existing database backups include these records; normal PostgreSQL backup/restore rules apply. Treat diagnostic backups as private: messages may contain media titles, local paths and account/device identifiers even after credential redaction.

Storage pressure evicts oldest batch bodies above **1 GiB globally** or **64 MiB per account/device**. These are logical compressed-body-plus-search-text budgets, not PostgreSQL disk quotas; indexes, row overhead and small duplicate receipts use additional space. Receipts survive body eviction until retention expiry so a lost acknowledgment cannot repopulate evicted logs. At most one million receipts are admitted; a device may submit at most 12 new batches per minute. Retries of an already accepted identical batch do not consume that rate allowance.

## Upload protocol and client recovery

`POST /api/client/v1/diagnostics/batches` uses the ordinary native access token and `X-Teddy-Compatibility-Version` compatibility header (currently `0.3`). Account and installation device identity come exclusively from the authenticated token grant. The payload cannot supply either identity. The request may use `Content-Encoding: gzip`; both compressed and decoded bodies are bounded to 256 KiB, with 1–100 entries and at most 8,192 UTF-8 bytes per message.

```json
{
  "batch_id": "d0716fae-a481-4017-909f-73cfa18b5797",
  "device_name": "Teddy android",
  "platform": "android",
  "app_version": "0.3.18",
  "entries": [{
    "id": "af8b350f-0b67-45ef-9804-d1c21d46bd42",
    "at": "2026-11-25T12:00:00Z",
    "level": "info",
    "component": "ted_playback",
    "message": "TED playback: phase=ready elapsed_ms=120",
    "session_id": "501def07-af08-426f-a415-382ffba1cb67"
  }]
}
```

Success returns `{"batch_id":"…","accepted":true,"duplicate":false}`. A matching retry returns the same acknowledgment with `duplicate:true`; changed content under the same account/device/batch identity returns 409. Invalid content returns 400/413/415; admission pressure returns 429 with `Retry-After: 60`. Unknown fields are rejected. The server independently redacts credentials and all HTTP(S) URLs before hashing, indexing or storing content. It stores no raw pre-redaction payload.

Teddy's Drift schema **34** persists stable batch IDs and exact redacted payloads before upload. It stages approximately 64 KiB/100-entry batches, writes pending logs after ten seconds (errors after 100 ms), and attempts up to four batches every fifteen seconds. Network failures retain the batch; retries back off from fifteen seconds to fifteen minutes. An older TED returning 404 causes a one-hour backoff without affecting playback. A malformed or rejected batch is dropped with a rate-limited local warning, avoiding an upload feedback loop. Only a matching accepted acknowledgment removes a successfully uploaded batch.

The local queue keeps at most **8 MiB per profile / 32 MiB per installation**, expiring after seven days. Memory staging and pending disk writes are bounded too; under pressure logs may be dropped. Profile switches fence and cancel uploads, retaining unsent old-profile batches for that same profile. Sign Out purges only that profile's diagnostic queue alongside its other scoped data. Opt-out clears all diagnostic queues on the installation. Uploads use the existing serialized `TedAuth` credential owner; they never refresh credentials independently or block player opening. Pre-authentication/startup failures remain in local logs, rather than being attributed to a later account.

## Admin query API

Native administrators use the existing access token at these endpoints. Browser administrators have equivalent `/api/admin/logs…` routes using the existing SCS session and CSRF protection for settings writes.

| Method and native path | Result |
| --- | --- |
| `GET /api/client/v1/admin/logs` | JSON entries, `next_cursor`, `head_seq` |
| `GET /api/client/v1/admin/logs?format=ndjson` | One record per line, attachment |
| `GET /api/client/v1/admin/logs/devices` | Latest labels/version/receipt time for up to 500 account/device pairs |
| `GET /api/client/v1/admin/logs/settings` | Retention, logical stored bytes and storage limits |
| `PUT /api/client/v1/admin/logs/settings` | Set `{"retention_days":90}` and return current settings |

Filters are `user_id`, `device_id`, `level` (debug/info/warning/error/fatal), `component`, `session_id`, `platform`, `app_version`, `q` (literal case-insensitive message substring, at most 200 characters), `since`, `until`, `after_seq`, `limit` and `cursor`. `since`/`until` are RFC3339 receipt timestamps; defaults are the last 24 hours through one minute ahead. Each record includes both device `at` and server `received_at`, authenticated `user_id`/`username`/`device_id`, labels, version, `batch_seq` and the structured entry. Device names currently use the platform label; use `device_id` to distinguish installations.

Results are newest received batches first, then reverse capture order within each batch. Limit defaults to 200 and is at most 500. A query scans at most 100 matching batches, so a page may be empty and still have a cursor. Continue using the returned opaque `next_cursor` **with the same original filters** until it is absent. Cursors retain a sequence snapshot so concurrent uploads cannot duplicate or shift older pages. There may be a final empty page. A cursor does not authorize access; every request rechecks the current administrator.

For polling, save `head_seq` and request `after_seq=<previous head>` with an appropriate receipt-time window. Drain any returned cursors before advancing to the new head, including after an empty page. NDJSON exposes pagination through `X-Next-Cursor` and `X-Log-Head-Seq` headers. The web UI offers a simpler fifteen-second refresh of the newest page. Downloads are bounded query pages, not unbounded exports.

## Logging coverage and limits

The November 25 audit inventories 650 non-generated Dart/native files (171,207 lines), then follows the active Teddy entry point and the owning error/effect boundaries. Existing meaningful logs are retained rather than adding per-frame/per-progress noise. The private inventory and test logs are under each repository's `.devbox/client-diagnostics-20261125`.

| Area | Coverage reviewed and changes |
| --- | --- |
| Startup and lifecycle | Framework/dispatcher errors, startup completion/failure, lifecycle/exit and active profile/platform/version phases. Pre-authentication events remain local. |
| Authentication and profiles | Existing secure-storage denial/uncertainty remains actionable; added successful restore/sign-in/refresh persistence and uncertain refresh phases. Switches fence uploads and preserve account separation. |
| API and catalogs | Fixed-category method/status/elapsed/response-size diagnostics; slow requests and HTTP failures visible normally. Success details and source-cache hit/miss/expiry are debug-only. No bodies, query strings, headers or provider URLs. Existing metadata/Home failure handling retained. |
| Playback and next episode | Existing preparation/retry/cache/relay/engine/route phases and session correlation retained. Detailed startup measurements are now selectable in settings. Native bridge failures report method and safe error code, never arguments; Android ExoPlayer fallback is visible. |
| Downloads and storage | Added resume/pause/complete/failure phases with stable IDs and safe codes. Added relocation/recovery start, completion and retained-journal failure diagnostics; no paths or per-byte logging added. |
| Viewing and offline sync | Added validated live/offline acknowledgment counts. Existing qualification/report ordering/recovery warnings remain; logging never creates viewing activity. |
| Updates and settings | Check, package preparation/verification and failure phases without download URLs or private paths. Diagnostic opt-out and debug controls are visible in Advanced. |
| Shared UI and native adapters | Existing actionable UI/image/track errors retained. Common Dart native invocation failures cover native adapters. Raw MPV output is not uploaded wholesale; bounded fixed-code native profiling retains its privacy boundary. |

This records application events while Teddy runs and is able to upload; it is not crash-dump collection, device-wide system logging or a guarantee against losing the final in-memory events during an abrupt process/device failure. Windows/Linux native OS crashes and physical Chromecast playback still require platform-specific acceptance. Automated persistence, HTTP, redaction and build checks do not establish end-to-end device delivery. No owner playback/history/provider actions are used as automated tests.
