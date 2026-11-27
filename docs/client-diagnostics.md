# Teddy diagnostics in TED

Teddy sends redacted application logs to its active TED server by default. Administrators open **Logs** in TED's web navigation to query them, download a bounded NDJSON page and change retention. Members can upload their own device logs but cannot read any logs, device inventory or diagnostic settings. Both browser and native API routes enforce this on the server.

In Teddy, **Settings → Advanced → Send diagnostic logs** disables uploads and removes queued diagnostic batches from that installation. Local diagnostic files remain available through Reveal logs. This setting is included in portable settings/reset; it does not change authentication, viewing reports, downloads or server history. Disabling cannot retract batches TED already accepted. **Detailed diagnostic logging** is off by default; enabling it also enables bounded startup/transport profiling for subsequently opened playback. Ordinary failures and actionable phases remain visible without it.

## Streaming delay measurements

Upgrade both TED and Teddy, enable **Detailed diagnostic logging**, then open playback again. Each physical relay attempt sends a new `X-TED-Client-Request-ID` UUID; TED returns an independent `X-TED-Request-ID`. The detailed-mode flag is `X-TED-Detailed-Media: 1`. These optional headers carry no credentials and grant no access. TED validates correlation IDs and enables details only after native authentication; old clients/servers remain usable. A header/connection failure can be joined by the client ID even before Teddy receives TED's response ID. The client `request` plus `upstream` counters distinguish retries within the same loopback response.

Teddy's existing `TED startup IO` records now include whole-session `transport_progress` every five seconds while requests remain active, with received/flushed byte totals and interval deltas, accumulated/max body/flush waits, offset, request age, active request count, generation, restart/cancellation state and validated correlation IDs. `wait_stage` is **0** handler work, **1** obtaining access headers, **2** opening the server connection, **3** waiting for response headers, **4** waiting for server body bytes, **5** flushing to the native reader, **6** closing its response and **8** retry backoff. `interrupting` counts pending upstream retirements. `complete` means the requested body was forwarded; `response_closed` separately records successful response close. Neither proves a decoded/displayed frame. `cancel_reason` is **1** native response finished/disconnected, **2** relay close, **3** final cleanup; a normal finished response also invokes cancellation. These fields avoid treating an old range's timeout or a normal cancellation callback as the active playback failure.

Packet samples retain the **256 samples / 1,024 startup events / 90 seconds** bounds. Non-packet transport milestones continue at up to **512 per minute per relay**, with a limit marker. Scheduled progress and one final summary per request bypass those sample/milestone allowances; summaries count suppressed milestones. Counters keep measuring after sampling ends. Detailed decoder buffering transitions and ordinary landing-watchdog failures include confirmed/decoded/requested positions, playing/buffering/loading state, source IDs and playhead generation, using existing player observations without extra native queries. No retry/watchdog/read timeout or playback policy changes.

TED writes JSON `msg="media delivery"` records to its ordinary container output. Every native playback/download media request has a final safe summary, including failures with detailed mode off. Specific safe RD rate-limit/credential/incomplete/missing-file/source-mismatch codes survive generic handler errors; raw provider errors are excluded. Detailed mode adds authorization, add-on/mapped session lookup, local-file/mapping locks, link cache/renewal, RD resolution, DNS/TCP/TLS/request/header milestones, first-byte/first-write and five-second progress. Upstream attempts and requested ranges are recorded numerically; cached/local/RD/add-on paths are labeled. Reads and downstream writes have separate byte totals and accumulated/max waits; `next_offset` is the next successfully written absolute file offset. A final source label can reflect the continuation after cached startup bytes; `range_selected` records the transitions. Detail phases are capped at **120 per minute per request**; progress and final summaries remain available after that cap. Existing Docker rotation bounds container log storage. These server records are **not** the uploaded client-log database shown by TED's Logs page.

Interpret matching IDs and byte counters together:

| Observation | What it establishes |
| --- | --- |
| TED waits in link/lock/lookup or connection/header phase | Delay before delivery, with the measured owning stage. |
| TED waits in `source_body`, with low source receipt | Upstream/local source is supplying bytes slowly at that read; inspect the source label and connection milestones. |
| TED waits in `downstream_write` or `downstream_flush` | Downstream backpressure at TED's connection, which may terminate at the proxy. |
| TED has written well ahead while Teddy waits at stage 4 | Delay/buffering after TED's accepted writes, between the proxy/network and client receipt. |
| Teddy has received data but waits at stage 5 | The local native reader is applying backpressure; check pause/cache/decoder state and whether the request is retiring. |
| Teddy forwards promptly but the landing watchdog fires | Inspect decoder position/source/buffering observations alongside native markers. A first-video marker alone is not a confirmed landing. |

Use `docker logs --timestamps --since <UTC-start> --until <UTC-end> ted-casaos-app-1` for a bounded installed-server window, then retain only the needed redacted JSON records. Search the session, `client_request_id` or `request_id`; query uploaded client logs with the corresponding `session_id` or `q`. Preserve both client log rotations for local collection. Do not print or retain authorization headers, private download links or unfiltered provider errors while collecting evidence.

The existing NPM access format records statuses/bytes but no upstream or request durations. Its temporary-file buffering warnings alone do not identify a fault. The optional [Nginx timing example](../deploy/nginx/ted-media-diagnostics.conf.example) adds one URL-free JSON completion record, keyed by TED's response ID, with connect/header/upstream/total durations and both byte counts. Its declarations belong in **http** context; the commented access directive belongs only in TED's **server** context, targeting the existing rotated access file. Merge through the proxy's supported configuration mechanism, preserve existing configuration and ordinary logging, validate `nginx -t` and roll out separately. It is not installed automatically. Upstream-time fields may contain multiple values or `-`, so the JSON keeps them as strings. Even matching endpoint/proxy logs cannot distinguish an ISP/router/Tailscale hop without concurrent network evidence, and container/OS failure can still lose final records.

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

## Agent access runbook

An agent can inspect uploaded logs for **all accounts on this TED server** using an administrator session and HTTP requests. It does not need a connection to the Chromecast, Mac or another Teddy device. Access is on demand while the agent is working; this feature does not itself schedule continuous monitoring. Only uploaded, retained logs are available, subject to opt-out, offline queues and the limits above.

### Confirm the server and authentication

The owner's current CasaOS origin is `http://100.122.31.92:33044` over Tailscale, or `http://192.168.1.2:33044` on the LAN. Port **33043 is the historical preview**, not this installation. On November 26, direct read-only checks of both current origins returned **TED 0.3.6 / compatibility 0.3** from `/api/client/v1/version`. An unauthenticated `/api/client/v1/admin/logs?limit=1` returned **401** on both routes. The old SSH control socket was no longer present; SSH is unnecessary when the origin is reachable directly.

The owner then created a separate administrator session through the local Terminal helper and supplied its directory path. On November 26, the agent validated `/api/auth/me` as administrator and successfully read `/api/admin/logs/settings`, `/api/admin/logs/devices` and a bounded log sample. The live setting was **90 days**; the inventory contained **one macOS installation/account pair**, with no Android pair present. Two cursor pages returned distinct entries under the same snapshot, the one-entry NDJSON body/cursor/head headers validated, and `after_seq` was accepted with no newer entries at the check. This establishes live authenticated browser-protocol API access; no native bearer credential or device connection was used.

Session handoff metadata is recorded only in the private, ignored `.devbox/log-access-20261126/active-session.json` (0600). Check its status and revalidate `/api/auth/me` before reuse; the saved path is not permanent access. On November 27 the prior session returned 401, the owner renewed access locally for The Quiet Earth investigation, and the agent then signed out only that separate diagnostic session, verified 401 and removed its temporary credential directory. The pointer now records `signed_out` without a session directory; a future investigation needs a fresh sign-in. The original November 26 probe summaries were removed by the owner-approved artifact cleanup; this runbook retains those verified results. No retention, viewing/history, provider or playback state was changed. Future investigations must use a valid administrator sign-in rather than assuming that an old SSH socket, a previous chat or an app's cached identity grants log access.

### Create a separate diagnostic session when needed

For interactive investigations, a separate browser-protocol session is convenient: it uses `/api/admin/logs…`, has the existing **24-hour absolute / 30-minute idle** limits and avoids sharing Teddy's rotating credentials. The owner can run this in Terminal from the TED project. Use the same origin as the working web sign-in: if that is an HTTPS reverse proxy, replace the HTTP example below with that HTTPS origin. Secure browser cookies are not sent over a plain HTTP backend origin; the private HTTP example requires TED's insecure-HTTP configuration. Enter the existing admin username/password locally, with an empty password only if that account actually has no password. Only the temporary session directory is printed; the password and cookie are not printed or put in command arguments/history. This is a new sign-in session, not a new account or a change to viewing/history.

```sh
export TED_LOG_ORIGIN='http://100.122.31.92:33044'
devbox run -- python3 - <<'PY'
import getpass, http.cookiejar, json, os, tempfile, urllib.request
from pathlib import Path

origin = os.environ['TED_LOG_ORIGIN'].rstrip('/')
with open('/dev/tty', 'w') as prompt, open('/dev/tty', 'r') as terminal:
    prompt.write('TED administrator username: ')
    prompt.flush()
    username = terminal.readline().strip()
password = getpass.getpass('TED password (blank only for a passwordless account): ')
jar = http.cookiejar.MozillaCookieJar()
client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
request = urllib.request.Request(
    origin + '/api/auth/login', method='POST',
    data=json.dumps({'username': username, 'password': password}).encode(),
    headers={'Content-Type': 'application/json', 'Accept': 'application/json'},
)
with client.open(request, timeout=15) as response:
    account = json.load(response)
if account.get('role') != 'admin':
    client.open(urllib.request.Request(origin + '/api/auth/logout-browser', method='POST'), timeout=15).close()
    raise SystemExit('Administrator access is required; this diagnostic session was signed out.')
with client.open(origin + '/api/auth/me', timeout=15) as response:
    current = json.load(response)
if current.get('role') != 'admin' or current.get('id') != account.get('id'):
    raise SystemExit('The diagnostic browser session did not validate.')
directory = Path(tempfile.mkdtemp(prefix='ted-log-session-'))
cookie_file = directory / 'admin.cookies'
cookie_file.touch(mode=0o600, exist_ok=False)
jar.save(str(cookie_file), ignore_discard=True, ignore_expires=True)
os.chmod(cookie_file, 0o600)
(directory / 'origin.txt').write_text(origin + '\n')
print(directory)
PY
```

The owner can give the agent the printed directory path; never paste the cookie/password into chat or commit it. The directory is private (0700), and the cookie file is 0600. This is a regular administrator credential; log inspection uses read-only content queries. Session files and downloaded logs belong outside tracked files. A failed sign-in is not permission to guess credentials. On 401, obtain a fresh sign-in; on 403, verify the account's administrator role. Do not extract or rotate Teddy's secure-storage tokens for this workflow. Native bearer-token access remains available through the native endpoints above, with its separate ten-minute access-token lifetime; never mix a browser cookie into native requests or a bearer header into browser requests.

### Discover devices and query logs

Set the session directory to the actual path printed above. Reading `origin.txt` keeps the cookie and origin paired. Run cookie-jar requests serially so concurrent writers cannot replace each other's saved state.

```sh
export TED_LOG_SESSION='/path/printed/by/the/sign-in/command'
export TED_LOG_ORIGIN="$(cat "$TED_LOG_SESSION/origin.txt")"

devbox run -- curl --silent --show-error --fail-with-body --max-time 15 \
  --cookie "$TED_LOG_SESSION/admin.cookies" --cookie-jar "$TED_LOG_SESSION/admin.cookies" \
  "$TED_LOG_ORIGIN/api/admin/logs/devices"

devbox run -- curl --silent --show-error --fail-with-body --max-time 15 \
  --cookie "$TED_LOG_SESSION/admin.cookies" --cookie-jar "$TED_LOG_SESSION/admin.cookies" \
  --get "$TED_LOG_ORIGIN/api/admin/logs" \
  --data-urlencode 'limit=200' \
  --output "$TED_LOG_SESSION/logs.json"
```

The second request queries all accounts over the default last-24-hours receipt window. Add `user_id` to isolate an account, `device_id` to isolate an installation across its accounts, or `session_id` for one playback. A device can appear once per account in the inventory. `platform=android` alone does not distinguish a Chromecast from another Android installation; identify its `device_id` before attributing a result. Other useful filters are `level`, `component`, `app_version` and `q`, for example `--data-urlencode 'q=phase=ready'`. Level is an exact match, not a minimum severity. Native/browser query parameters and pagination semantics are identical.

For a line-oriented export, add `--data-urlencode 'format=ndjson'`, save the body as `logs.ndjson` and use `--dump-header "$TED_LOG_SESSION/headers.txt"` for the cursor/head headers. These headers may also contain a session cookie: keep them in the private directory. Follow `next_cursor`/`X-Next-Cursor` with the same filters even when a page is empty. Do not assume that the first 200 results are the complete incident.

To follow new arrivals, poll about every fifteen seconds with `after_seq` set to the last **fully drained** `head_seq`. For each poll, choose a fresh upper receipt-time bound (or the default), then keep that window fixed across its cursor pages. Advance the watermark only after draining the entire snapshot. A narrower moving `since` can omit arrivals outside that window; retain the intended incident window when completeness matters. Deduplicate saved records by `(batch_seq, id)` if polling is retried after uncertain local processing. Stopping the task stops monitoring; do not leave a background watcher running unless requested.

Investigate using original `at`, `received_at`, session/media IDs and measured phase fields such as `elapsed_ms`. Receipt order is upload order, not a playback timing measurement; delayed uploads and device clock differences must not be called player startup latency. Ordinary logs may not include detailed startup phases. Ask for **Detailed diagnostic logging** to be enabled before a new reproduction if those measurements are needed. Missing entries alone do not prove that playback, pre-warm or caching failed.

When the investigation ends, revoke only this diagnostic browser session and remove its private directory:

```sh
devbox run -- curl --silent --show-error --fail-with-body --max-time 15 \
  --cookie "$TED_LOG_SESSION/admin.cookies" --cookie-jar "$TED_LOG_SESSION/admin.cookies" \
  --request POST "$TED_LOG_ORIGIN/api/auth/logout-browser"
```

After successful sign-out, delete the exact temporary directory created for this session, retaining only any explicitly needed redacted evidence. Do **not** use `/api/auth/logout`: that legacy all-device action revokes the account's other sessions too. Diagnostic content queries are GET requests; routine inspection does not change retention, upload logs, create playback sessions, report viewing or add RD files.

## Logging coverage and limits

The November 25 audit inventories 650 non-generated Dart/native files (171,207 lines), then follows the active Teddy entry point and the owning error/effect boundaries. Existing meaningful logs are retained rather than adding per-frame/per-progress noise. The private inventory and local test logs were under each repository's `.devbox/client-diagnostics-20261125`; they were removed in the owner-approved November 26 cleanup. The code/tests, logging coverage description and hosted release results remain available.

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
