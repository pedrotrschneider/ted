# Releases and compatibility

TED and Teddy have independent GitHub repositories and independent `version.txt` files, initially `0.1`. Each file contains exactly MAJOR.MINOR. CI reserves patch and candidate numbers in annotated Git tags, not commits to the version file. This supersedes inherited Plezy release tooling and private-only distribution instructions. Do not use Teddy's inherited `scripts/release/deploy.py` or `release.sh` to publish Teddy; they target upstream store/update services.

## Compatibility contract

The major is the shared compatibility boundary: TED 2.x.y requires Teddy 2.z.w. Minor and patch releases advance independently and must remain compatible in both directions within their major. New optional features need capability detection and graceful fallback, not an implicit minimum minor version. Breaking changes require bumping both projects' major versions. This explicit promise also applies to major zero.

TED exposes public `GET /api/client/v1/version` with `version` and `compatibility_major`. Native requests send `X-Teddy-Compatibility-Major`; mismatches receive HTTP 426 with `incompatible_version` before login or route execution. TED returns `X-TED-Compatibility-Major` on native responses. Existing clients without the header are major zero, preserving the current deployment. The `/v1` route protocol number is separate from the product compatibility major. Offline files remain locally accessible; this is not a credential purge.

The container embeds version.txt and its allocated full version. Release Teddy builds receive compile-time compatibility/version definitions and show the full version, including rc suffix, in About. Native platform version fields use the numeric core; Android/Apple build numbers use 1000 + the release workflow run number, which survives reruns and increases across minor/major bumps. Local Mac builds derive their major and development version from version.txt. For other manual Flutter builds pass `--dart-define=TEDDY_COMPATIBILITY_MAJOR=<major>` and `--dart-define=TEDDY_RELEASE_VERSION=<version>`; the committed defaults are 0 and 0.1.0-dev.

## Allocation examples

| Event | Reserved version |
| --- | --- |
| First main build for 0.1 | 0.1.0 |
| Develop after 0.1.0 | 0.1.1-rc1, 0.1.1-rc2, … |
| Next main build | 0.1.1 |
| Develop after that main reservation | 0.1.2-rc1 |
| Change version.txt to 0.2 | 0.2.0 / 0.2.0-rc1 |
| PR #7 | next-patch-pr7.RUN_ID (artifact only) |

Both branches share a queued workflow concurrency group. GitHub's `queue: max` retains up to 100 pending runs, unlike the default that replaces pending runs. Reservations happen before builds, so failed/cancelled runs can leave gaps. Rerunning the same workflow run reuses its annotated reservation, even after newer releases exist. Do not delete or rewrite version tags: they are the durable version ledger. Never force-push a reservation. Each push builds the pushed tip, as usual for GitHub push workflows.

Tags use `vVERSION`; image tags omit v. Development versions use GitHub prereleases and never become Latest. Main Teddy releases become Latest. An older rerun cannot promote its version past a newer reserved version in that channel. Existing public Teddy assets and already-published TED version images are preserved on reruns, avoiding mutable version artifacts and moving channel aliases backwards. TED's immutable-version check fails closed on registry errors other than missing manifests. A failed image push that published a version but not its channel alias may need the alias repaired manually to that same digest.

## Outputs

- **TED:** `ghcr.io/pedrotrschneider/ted:VERSION`, plus `:latest` for main or `:develop` for develop. AMD64 and ARM64 manifest; no GitHub Release object. All web/Go checks must pass before building/publishing. PRs build the same multiarchitecture image as a downloadable OCI tar artifact without logging into GHCR or publishing.
- **Teddy:** universal macOS ZIP, Windows x64 portable ZIP, Linux x64 and ARM64 portable tarballs, universal Android APK (ARM32/ARM64/x64; phones and Android TV share the APK). No iOS. Each platform supplies SHA-256 checksums. The release stays draft until every build and artifact upload completes. PRs upload the same packages as Actions artifacts only; their Android APK uses the runner's temporary debug key.
- Retention: Teddy Actions artifacts 14 days; TED PR OCI artifact 7 days. Published release assets remain until explicitly removed. CI performs no home-server deployment or owner-machine app launch.

macOS ZIPs are ad-hoc signed, not Apple-notarized; Windows ZIPs are unsigned. These are sideload packages and OS trust prompts can apply. Apple Developer and Windows signing credentials are not configured or fabricated. Linux is built on Ubuntu 24.04, bundles the locked libmpv and supporting libraries, and still requires compatible host graphics/audio libraries, glibc, and an unlocked libsecret keyring. Launch the `Teddy` wrapper inside the extracted folder. Windows currently builds x64 only. Build success is not runtime acceptance on every target.

Android uses application ID `com.entertainmentdistrict.teddy` (Kotlin namespace remains inherited). This installs separately from old Plezy-ID APKs and does not migrate their private data. All published APKs must use the same retained signing key. The workflow deliberately fails rather than publish an unsigned or newly debug-signed release.

## Repository setup

Push each project's files to its own repository, with main/develop branches as appropriate. The workflows use GITHUB_TOKEN with narrowly scoped contents/packages permissions; no personal access token is needed. Repository/organization policies and tag rules must permit the workflows to create `v*` tags, Teddy releases, and TED GHCR packages. GHCR packages may initially be private; set visibility as desired in package settings. Existing packages must grant the TED repository Actions write access.

In **Teddy → Settings → Secrets and variables → Actions**, configure:

| Secret | Value |
| --- | --- |
| ANDROID_KEYSTORE_BASE64 | Base64 of the retained release JKS/keystore, without line wraps |
| ANDROID_KEYSTORE_PASSWORD | Keystore password |
| ANDROID_KEY_ALIAS | Signing alias |
| ANDROID_KEY_PASSWORD | Alias/private-key password |

Keep a secure offline backup of that keystore. CI writes it only for trusted push builds and removes temporary signing files in an always-run cleanup step. PR builds never receive release signing secrets. There is no pull_request_target execution of untrusted code.

Flutter is pinned to the project's 3.47.0 toolchain. Linux ARM64 uses flutter-action's Git installation mode (`channel: master`) at that exact release tag because no stable SDK archive is available for this target; it does not follow moving master. macOS uses the macos-26 runner and verifies SDK 26 or newer, required by the locked connectivity_plus Swift code. Android setup explicitly installs `platform-tools` rather than the removed `tools` package; Java 21, the project's Android NDK/CMake/build-tools pins, and Python 3.12 are installed on hosted native runners. Linux libmpv and other native downloads retain their existing checked-in checksum validation. No new application runtime dependency was added.

## Validation and recovery

Run `python3 -m unittest discover -s scripts -p test_release_version.py` in either repository. Tests cover reset/increment rules, invalid input, numeric ordering, and an actual isolated Git remote for reservation/rerun/PR behavior. The allocator files/tests intentionally match across independent repos; update both when release policy changes.

Use actionlint for both workflows. Version 1.7.12 does not yet recognize GitHub's documented `concurrency.queue`; until it does, ignore only that exact diagnostic, not all syntax diagnostics. GitHub documentation: https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency .

On failure, use Re-run failed jobs or Re-run all jobs in the original run; do not manually bump patch numbers or delete reservations. Teddy upload failures leave a draft that the same run can finish. A missing Android secret blocks publication of the entire Teddy release until configured. Hosted runs and OS runtime tests remain necessary before declaring cross-platform support verified.

## In-app update discovery and installation

Teddy Settings → Look for Updates stores a Stable (default) / Release Candidate channel. Published, non-draft GitHub releases are compared numerically, including rc10 > rc2 and final > candidate. The RC channel includes final releases. No downgrades or automatic compatibility-major jumps are offered. Development versions remain manual. Checks run when opening this section or changing channel, with a manual retry action; failures do not interfere with playback. Discovery reads the latest 100 published releases plus GitHub’s Latest release, not reserved tags.

Update to version… downloads the matching package from the official repository over HTTPS and verifies its published SHA-256. macOS also verifies the unpacked app signature; Android delegates package/signature/version enforcement to the OS. The existing archive package is now a runtime dependency at its locked version; no new updater service or upstream Plezy update feed is used.

Desktop updates require a writable installation parent. They stage on the same filesystem, wait for orderly player/download/database shutdown and process exit, move the old bundle aside, install, and relaunch. Replacement failure restores the previous bundle. Portable Windows/Linux packages include teddy-release.json; missing manifests or extra top-level personal files refuse replacement. A configured download directory inside the active app bundle also blocks updating. Accounts, preferences and downloads outside the bundle are retained. There is no elevation prompt or unattended forced exit. Staging/previous bundles and install.log remain in a sibling .teddy-update-* directory for manual recovery; backups are not automatically deleted. A launch/crash after a successful replacement is not an automatic health-checked rollback. Native installation acceptance remains required on each OS.

Android asks for Install unknown apps permission if needed, then opens the system APK confirmation. Retry uses the verified staged APK after permission is granted. Published APKs must retain the existing signing key. Store-distributed variants and iOS are not part of this updater.

TED exposes admin-only GET /api/admin/updates and POST /api/admin/updates/dismiss. It reads the published GHCR latest image (develop for an installed rc), including the image config’s OCI version label, so failed builds/reserved tags cannot trigger notices. Successful checks cache six hours; failures back off five minutes. Local dev versions do not compare as releases. Admin browsers check on sign-in and hourly, displaying a bottom-center notification with no installation action. Dismissal is stored per admin in PostgreSQL for 24 hours across browsers and versions. Other users cannot check/dismiss via these routes; there is no permanent disable preference. Checking a newer TED major may announce it, but operators must upgrade compatible Teddy clients themselves.

## Application icons

Each repository’s root icon.png is its artwork source. TED’s web prebuild copies it to public/icon.png, and Docker includes both the source and the sync script. Teddy uses generated macOS AppIcon assets, a multi-resolution Windows ICO, Android density/adaptive assets, and the PNG bundled with its Linux window/runtime. Regenerate Teddy raster resources on macOS with `devbox run -- python3 scripts/generate-app-icons.py`; no third-party imaging package is required. The source image is 512px; the 1024px Mac resource is upscaled. Existing iOS resources are unchanged.
