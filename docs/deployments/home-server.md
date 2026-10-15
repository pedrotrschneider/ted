# Owner home-server deployment

Updated October 15, 2026. This records the installed native deployment, not broad platform acceptance. The old Mac preview is stopped; do not run a second copy of its synchronization workers. General backup/restore is in [Operations](../operations.md); code/test status is in [IMPLEMENTATION](../../IMPLEMENTATION.md).

## Current installation

| Item | Value |
| --- | --- |
| Host | `ptrschneider@192.168.1.2`, Linux x86_64 |
| Web/LAN | `http://192.168.1.2:33043` |
| Deployment root | `/home/ptrschneider/projects/ted-teddy-preview` |
| Compose | project `ted-teddy-preview`; `compose.yaml`, `compose.home.yaml`, private `.env` |
| Image | `ted-home:20261015-person-cursor` |
| Image ID | `sha256:1c0e761c2db40c069a8b475dae4903ccb8261f9efe8a1bba609a329f8fb196f6` |
| Source | `releases/20261015-person-cursor` |
| Source archive SHA256 | `808d84c4cd72f413da96bbb37b18a62f2bce89f749883f3e9a6111e9b5853bed` |
| Database | PostgreSQL 18; TED schema 41; host Config/postgres |
| Files | Config/metadata and Media bind mounts; historical Config/recovery retained on disk, no active recovery mount |
| New Mac build | `../teddy/build/releases/20261015T142015Z-vVCwj0du/Teddy.app` relative to the local TED folder; rebuilt after relocation, signed, owner launch pending |

Provider credentials are in the database/admin settings, not frontend files. `.env` holds deployment configuration/database password. Preserve the Compose override's `default` database network **and IPv6-enabled `egress` network**. Earlier IPv4-only container egress caused intermittent add-on/RD connection stalls even when host IPv6 requests succeeded; dual-stack egress was part of the accepted reliability correction. Other hosts must verify their route rather than copying this assumption blindly.

## Latest rollout

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

.devbox/joint-filter-20261015 holds the latest web/API/race/deployment evidence; `.devbox/home-watch-20261015` holds the previous Home/watch evidence; retirement evidence `.devbox/retirement`. The temporary home-server `ted-source-cache-test` database was tmpfs-only with no volumes; it and its dedicated 37968 SSHforward were removed after checks. Production 33043 remains.

Historical 14–22 second decode-command wall times included network/open/range/resume work, not demonstrated decoder computation. Subsequent network/startup/prefetch fixes were owner-accepted. Further opportunities are in [the startup review](../startup-and-account-review-2026-10-14.md); do not report old failure measurements as current native first-frame timings.
