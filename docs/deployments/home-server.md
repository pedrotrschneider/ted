# Owner home-server deployment

Updated October 17, 2026. This records the installed home-server preview on port 33043, not broad platform acceptance. The separate CasaOS installation on port 33044 was observed running `ghcr.io/pedrotrschneider/ted:0.1.9` and was not changed by this preview rollout. The old Mac server is stopped; do not start its stale synchronization workers. General backup/restore is in [Operations](../operations.md); code/test status is in [IMPLEMENTATION](../../IMPLEMENTATION.md).

## Current installation

| Item | Value |
| --- | --- |
| Host | `ptrschneider@192.168.1.2`, Linux x86_64 |
| Web/LAN | `http://192.168.1.2:33043` |
| Deployment root | `/home/ptrschneider/projects/ted-teddy-preview` |
| Compose | project `ted-teddy-preview`; `compose.yaml`, `compose.home.yaml`, private `.env` |
| Image | `ted-home:20261017-series-user-indicators` |
| Image ID | `sha256:f6f9224bc7fb58c1deb0af015541dc85ec81b0d2e9bf52648576ee9603047e12` |
| Source | `releases/20261017-series-user-indicators` |
| Source archive SHA256 | `4c5f3d76385d24fa89039e2fd6b8674241f9d93df99932d4e0f3990e28bdb934` |
| Reported version | `0.1.0-preview.20261017.user-indicators`, compatibility major `0` |
| Database | PostgreSQL 18; TED schema 44; host Config/postgres |
| Files | Config/metadata and Media bind mounts; historical Config/recovery retained on disk, no active recovery mount |
| New Mac build | `../teddy/build/releases/20261017T232027Z-6WCWQKoR/Teddy.app` relative to the local TED folder; built/signature-verified, not launched or installed by this rollout |

Provider credentials are in the database/admin settings, not frontend files. `.env` holds deployment configuration/database password. Preserve the Compose override's `default` database network **and IPv6-enabled `egress` network**. Earlier IPv4-only container egress caused intermittent add-on/RD connection stalls even when host IPv6 requests succeeded; dual-stack egress was part of the accepted reliability correction. Other hosts must verify their route rather than copying this assumption blindly.

## Latest rollout

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
  ptrschneider@192.168.1.2
```

Do not repeat while the forward owns 33043. Check `curl --fail http://127.0.0.1:33043/health/ready`. Agent shell operations use the same ControlPath; re-establish via owner login if the master expires. Do not guess passwords or weaken host-key checking.

## Retained migration and evidence

The Mac preview moved to this server on October 13/14. The stopped migration snapshot matched 79 public-table fingerprints and 1167 durable-file hashes at then-schema 37. These are historical migration results, not the current schema/counts. Migration backups `ted.dump`, `durable-files.tar.gz` and `deployment.env` remain under backups. Other existing server services were left intact.

All Colima VMs were deliberately stopped; inventory found one running profile (`default`) and helper processes. Disks/data were preserved. The Mac fixture remains `.devbox/test-evidence/2026-10-10T20-35-36.441Z-ted-native-test-89464-35183799`, migration evidence `.devbox/test-evidence/ted-home-server-2026-10-14`. Starting it now would introduce stale state and duplicate sync. Do not treat it as an active dependency or current rollback without reconciliation.

Latest deployment evidence is `.devbox/preview-indicators-20261017`; `.devbox/preview-playback-20261017` retains the previous rollout; `.devbox/joint-filter-20261015` and `.devbox/home-watch-20261015` retain earlier evidence; retirement evidence is `.devbox/retirement`. The temporary home-server `ted-source-cache-test` database and its dedicated 37968 SSH forward were removed after those checks. The preview remains on 33043; CasaOS is separate on 33044.

Historical 14–22 second decode-command wall times included network/open/range/resume work, not demonstrated decoder computation. Subsequent network/startup/prefetch fixes were owner-accepted. Further opportunities are in [the startup review](../startup-and-account-review-2026-10-14.md); do not report old failure measurements as current native first-frame timings.
