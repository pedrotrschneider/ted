# TED operations and recovery

Updated October 15, 2026. Read [README](../README.md) for a new installation and [the home-server record](deployments/home-server.md) before operating the owner's actual service. Commands here assume the repository's default Compose project/files; add the installed project's name/override flags where required. Run development-machine commands in Devbox; the deployment server already has Docker/Compose.

## Configuration

[.env.example](../.env.example), [compose.yaml](../compose.yaml) and [internal/config](../internal/config) are the executable reference. Keep `.env` private. Never put provider keys in `VITE_` variables or frontend assets.

| Setting | Purpose/default |
| --- | --- |
| TED_DB_PASSWORD | Compose PostgreSQL password; use a generated URL-safe hexadecimal value. Editing it does not change an existing cluster's password. |
| TED_CONFIG_DIR | Host Config root, default ./Config; database under postgres, artwork under metadata. Changing it does not move data. |
| TED_MEDIA_DIR | Compose host media root, default ./Media, mounted /media. Direct Go execution requires an absolute path; unconfigured local storage disables local-download support. |
| TED_UID / TED_GID | App UID/GID, default 10001:10001; match writable media/metadata ownership. Do not make folders world-writable as a workaround. |
| TED_PORT / TED_BIND_ADDRESS | Host 8080 /127.0.0.1. Deliberate LAN exposure may use 0.0.0.0. Database stays internal. |
| TED_INSECURE_HTTP | false; cookies require HTTPS. true is deliberate local/LAN HTTP, not TLS support. Put HTTPS termination in a reverse proxy. |
| TED_TIMEZONE | UTC; IANA timezone for release-day eligibility. |
| TED_MIN_PASSWORD_LENGTH | 0; optional passwords. Configured minimum applies to new/changed passwords, not existing password verification. |
| TED_ACCESS_TOKEN_TTL | 10m; native access lifetime. |
| TED_REFRESH_TOKEN_IDLE_TTL | 720h (30 days); inactivity cap. |
| TED_TOKEN_FAMILY_TTL | 2160h (90 days); absolute cap, never extended by rotation. Access must be shorter than inactivity, which is <= absolute. |
| TED_MAPPING_BATCH_SIZE / TED_MAPPING_WORKERS | 200 /4; ranges 1–500 /1–4. Separate from Teddy's device-download limit. |
| TED_METADATA_LANGUAGE / TED_METADATA_COUNTRY |en /US; descriptive locale/certification country, not original audio language. |
| TED_REAL_DEBRID_API_KEY | Legacy one-time credential import; use administrator provider settings. Explicit removal must not resurrect an environment key. |
| TED_TMDB_ACCESS_TOKEN / TED_TVDB_API_KEY / TED_TVDB_PIN | Direct Go legacy one-time imports. Prefer administrator settings; Compose does not require these secrets in its environment. |
| TED_DATABASE_URL / TED_DATABASE_URL_FILE | Direct Go: exactly one connection source. Compose supplies TED_DATABASE_URL. |
| TED_METADATA_DIR | Direct Go artwork directory; Compose maps Config/metadata to /metadata. |
| TED_WEB_DIR | Built trusted frontend, web/dist by default; image uses /opt/ted/web. Never point it at uploads. |
| TED_LISTEN | Direct Go listener, :8080. Compose fixes internal port 8080. |

Provider settings store RD/TMDb/TVDB secrets in the database; Stremio discovery and history credentials are separate database state. Saved values are not returned to the browser. Browser SCS defaults are 24-hour absolute/30-minute idle sessions; native tokens have separate lifetimes above.

Startup applies embedded forward-only migrations under a PostgreSQL advisory lock, then starts services/workers. Failed migration stops startup; inspect the redacted phase/SQLSTATE, do not reset the database. Schema 41 is the current server checkpoint. An older binary cannot safely run against a newer incompatible schema.

## Persistent boundaries

PostgreSQL 18 uses Config/postgres mounted at `/var/lib/postgresql` (its 18/docker layout). Metadata uses Config/metadata; owned media and resumable staging use the configured media filesystem. Retain historical Config/recovery on disk even though no active app mount needs it. Compose has no durable named/anonymous volumes, read-only app root, temporary /tmp and rotated logs (10 MiB ×3 per service).

River owns mapping/download/metadata/Stremio/RD-inventory/cleanup work. Restart is not cancellation. Preserve queued jobs, selected source descriptors, file checkpoints, replay lineage and provider/account generations. Do not execute copied workers against the same external accounts during a restore drill.

Teddy device files, Drift state and OS credential bundle are separate from server backups. Device-local media deletion retains pending reports; profile Sign Out deliberately purges them. Settings exports are not authentication/download/history backups and intentionally omit device paths.

## Inspect, update and stop

In the correct deployment directory:

```sh
docker compose ps
docker compose logs --tail=100 app
docker compose exec -T app ted healthcheck
```

Use an idle window before updates when practical. Take a backup, build a new image, retain the old image/Compose configuration and start the new app against the existing host files. Confirm readiness, schema and preserved counts. Do not replace running native app bundles; use Teddy's isolated release builder. The owner's service has its own immutable image naming and dual-stack network override; preserve both networks when updating it.

`docker compose down` stops the app/database but preserves bind-mounted files. Never remove Config/Media as container cleanup. Do not use `down` on an unrelated/default project when managing the owner's named project.

## Consistent backup

A filesystem copy of a running PostgreSQL directory is not a valid general backup. Either stop the entire stack and copy the full deployment/storage with permissions, or stop the app and take a logical dump with matching metadata/media. Keep `.env`, Compose/overrides, source/image identity, lockfiles/notices and recovery archives. Ensure backup destination capacity for media; a DB dump alone lacks the bytes.

For the default layout, enter Devbox Bash locally (or Bash on the server) and run:

```bash
set -e
umask 077
ted_backup_dir="Config/backups/manual-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p Config/backups
mkdir "$ted_backup_dir"
docker compose stop app
docker compose exec -T db pg_dump -U ted -d ted -Fc > "$ted_backup_dir/ted.dump"
docker compose exec -T db pg_restore --list < "$ted_backup_dir/ted.dump" > "$ted_backup_dir/contents.txt"
cp .env "$ted_backup_dir/deployment.env"
cp compose.yaml "$ted_backup_dir/compose.yaml"
cp -a Config/metadata "$ted_backup_dir/metadata"
if test -d Config/recovery; then cp -a Config/recovery "$ted_backup_dir/recovery"; fi
cp -a Media "$ted_backup_dir/Media"
docker compose start app
```

Adapt configured paths and include installed overrides; the owner deployment requires its `-p`/`-f` flags. These backups contain credentials and session state. If any step fails, the app stays stopped; keep the evidence and explicitly decide when to restart. Retention/deletion of owner backups is not approved by source cleanup.

A stopped full-file copy requires compatible PostgreSQL 18 and CPU/platform layout, preserving ownership. For a major-version/platform move use supported PG migration or logical restore. Never bootstrap a restored database. Never run old and restored Stremio sync copies simultaneously.

## Isolated logical restore inspection

Use a new empty destination and only start PostgreSQL, not copied application workers. In a new Bash shell, using the default Compose layout and replacing CHOSEN-BACKUP:

```bash
set -e
export TED_CONFIG_DIR="$PWD/Config-restore-test"
export TED_MEDIA_DIR="$TED_CONFIG_DIR/Media"
export TED_PORT=8082
if test -e "$TED_CONFIG_DIR"; then echo 'Choose a new empty restore directory'; exit 1; fi
mkdir -p "$TED_CONFIG_DIR/postgres"
cp -a Config/backups/CHOSEN-BACKUP/Media "$TED_MEDIA_DIR"
cp -a Config/backups/CHOSEN-BACKUP/metadata "$TED_CONFIG_DIR/metadata"
docker compose -p ted-restore up -d --wait db
docker compose -p ted-restore exec -T db pg_restore --exit-on-error --no-owner --no-acl -U ted -d ted < Config/backups/CHOSEN-BACKUP/ted.dump
docker compose -p ted-restore exec -T db psql -U ted -d ted -c 'SELECT count(*) FROM users; SELECT count(*) FROM media_mappings; SELECT count(*) FROM watch_history;'
```

Check metadata/media checksums and ownership against the backup manifest too. `docker compose -p ted-restore down` stops only that test project and retains restored host files. Exit this shell before managing production so path/port overrides cannot leak into production commands.

Before exposing an **older restored snapshot**, invalidate restored login grants to avoid resurrecting consumed/revoked sessions. Against the restored DB only, before starting the app:

```sh
docker compose -p ted-restore exec -T db psql -U ted -d ted -v ON_ERROR_STOP=1 -c "BEGIN; UPDATE users SET session_version=session_version+1; DELETE FROM sessions; UPDATE native_token_families SET revoked_at=COALESCE(revoked_at,clock_timestamp()),revocation_reason=COALESCE(revocation_reason,'logout'); COMMIT;"
```

Keep refresh-token/event lineage; do not truncate it. Require fresh login after this. Session revocation does not reconcile newer watch decisions, source generations or client pending reports against an old snapshot. Resolve mismatched restore points before reconnecting clients/external sync. A cutover must stop the old application and use the selected restored storage/configuration, not simply start both copies. Review preserved jobs/credentials before allowing their external effects.

The current isolated schema 40→41 rehearsal preserved all 74 retained-table fingerprints. The broader prior stopped-copy recovery evidence is historical; full latest-profile/offline/root-migration recovery across OS reinstall remains a release gap. `scripts/test-portable-restore.mjs` is a diagnostic harness with private snapshots and an isolated network, not a production restore command or proof that copied workers cannot mutate their local DB. Review it against the current schema/jobs before running.

## Older named-volume installations

Never start an old deployment against a fresh empty Config path. `scripts/migrate-config-storage.mjs` exists for the earlier named-volume foundation: it backs up/stages the cluster and preserves the old volume. It is not needed on the current bind-mounted home server. Inspect source mounts/version, take a backup and review the helper's assumptions before using it on another deployment. A preserved old volume becomes stale after new writes and is not a substitute for current backups.

## Diagnostics and safety

Use bounded logs, useful job phases and redacted provider errors. Do not log secrets/private source URLs or dump `.env`/credentials in chat. Private `.devbox` snapshots may include authentication and history. Database test schemas must run in a disposable DB, with `TED_REQUIRE_DATABASE_TESTS=1`; Go packages serialize with `-p1` because advisory locks are database-wide. Remove only the exact disposable resources created for the test; preserve production tunnels/data.

Health/web 200 confirms process routing, not playback compatibility. Verify selected release, ranges, renewal, actual duration and native first-frame separately. The owner's dual-stack egress correction addresses a measured network failure mode; do not infer that all slow startup is decoding or that switching CDN aliases is safe. Further measurements are tracked in the deferred startup document.
