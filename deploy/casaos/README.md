# Independent TED installation on CasaOS

Uses `ghcr.io/pedrotrschneider/ted:latest` and PostgreSQL 18, with a fresh database. The development stack remains untouched on port **33043**. This installation uses **33044** (checked unused when generated), project `ted-casaos`, and its own networks, accounts and provider settings.

## Filesystem storage

All durable application data uses explicit host bind mounts, with no named or anonymous Docker volumes:

```text
/DATA/AppData/ted/
  docker-compose.yml   # Keep your filled-in Compose file here for backups
  postgres/            # Entire PostgreSQL 18 data directory
  metadata/            # Artwork and metadata files
  media/               # Server-owned media and staging files
```

Accounts, credentials, history, mappings and jobs live in PostgreSQL. Its mount is `/var/lib/postgresql`, matching the PostgreSQL 18 image's declared volume, so Docker does not create an anonymous data volume. App `/tmp` is temporary memory-backed scratch space; it does not need backing up. Docker's container logs and writable runtime layers are disposable and not required for restoring TED.

## Install

1. Generate a new database password, for example `openssl rand -hex 32`. Replace **both** `REPLACE_WITH_NEW_HEX_PASSWORD` values with that same password. Do not reuse the development password or commit the filled-in file.
2. Prepare the folders on the server:

   ```bash
   sudo mkdir -p /DATA/AppData/ted/{postgres,metadata,media}
   sudo chown 1000:1000 /DATA/AppData/ted/metadata /DATA/AppData/ted/media
   ```

   The app runs as UID/GID 1000. PostgreSQL's entrypoint initializes ownership of its own bind directory; do not recursively change its ownership to the app user.
3. Save the filled-in Compose as `/DATA/AppData/ted/docker-compose.yml`, restrict it to the administrator (`chmod 600`), and import it through CasaOS Custom Install. If you later edit the app in CasaOS, export/save that updated Compose into this folder before backing up.
4. Open **http://192.168.1.2:33044** after both services become healthy. On an empty installation, create the first administrator in the setup page, then sign in and configure providers. This requires an image containing web setup; older images offer only CLI bootstrap. Leave the development containers running; no migration or shared database is involved.

The database has no published host port. The IPv6-enabled egress network preserves the networking approach needed for reliable RD/add-on access on this host. LAN HTTP is enabled; set TED_INSECURE_HTTP to "false" when serving through HTTPS.

The previous registry check returned `unauthorized`. Before import, make the TED GHCR package public or authenticate Docker on the server with package read access, and confirm the stable workflow has published `latest`. Repository visibility and package visibility are separate.

## Copy-based backup and restore

**Stop this entire stack before copying its folder.** Copying a running PostgreSQL directory is not a consistent backup. The development stack can remain running.

```bash
cd /DATA/AppData/ted
sudo docker compose -p ted-casaos -f docker-compose.yml stop
sudo rsync -aHAX --numeric-ids /DATA/AppData/ted/ /your-backup-location/ted/
sudo docker compose -p ted-casaos -f docker-compose.yml start
```

Ensure the copy finishes successfully before restarting. Backups contain account/provider secrets; keep them private. Preserve numeric ownership and permissions. For restore, stop any existing target stack, restore the complete folder to `/DATA/AppData/ted`, and import/start the saved Compose. Do not merge an old backup into a newer database directory. Retain PostgreSQL major version 18 unless performing a proper database upgrade. Record the exact TED image digest with each backup; for recovery use the matching image first, then upgrade deliberately. Since `latest` moves, pulling it at restore time may introduce migrations.

`pull_policy: always` checks for the latest stable image on Compose up/recreate; it does not update running containers in the background. Use CasaOS update/recreate after backups. A future TED major requires the matching Teddy major.

Generated and Compose-validated only; not deployed. CasaOS metadata follows its [official example](https://github.com/IceWhaleTech/CasaOS-AppStore/blob/main/Apps/HomeAssistant/docker-compose.yml).
