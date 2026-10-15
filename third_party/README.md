# Go runtime dependency notices

Generated from the pinned modules used by `cmd/ted`. Original license and notice files follow in the linked directories. Regenerate with `devbox run -- node scripts/licenses.mjs` after changing dependencies.

| Module | Version | Notices |
| --- | --- | --- |
| github.com/alexedwards/argon2id | v1.0.0 | [LICENSE](github.com_alexedwards_argon2id/LICENSE) |
| github.com/alexedwards/scs/pgxstore | v0.0.0-20251002162104-209de6e426de | [LICENSE](github.com_alexedwards_scs_pgxstore/LICENSE) |
| github.com/alexedwards/scs/v2 | v2.9.0 | [LICENSE](github.com_alexedwards_scs_v2/LICENSE) |
| github.com/jackc/pgpassfile | v1.0.0 | [LICENSE](github.com_jackc_pgpassfile/LICENSE) |
| github.com/jackc/pgservicefile | v0.0.0-20240606120523-5a60cdf6a761 | [LICENSE](github.com_jackc_pgservicefile/LICENSE) |
| github.com/jackc/pgx/v5 | v5.11.0 | [LICENSE](github.com_jackc_pgx_v5/LICENSE) |
| github.com/jackc/puddle/v2 | v2.2.2 | [LICENSE](github.com_jackc_puddle_v2/LICENSE) |
| github.com/mfridman/interpolate | v0.0.2 | [LICENSE.txt](github.com_mfridman_interpolate/LICENSE.txt) |
| github.com/pressly/goose/v3 | v3.28.0 | [LICENSE](github.com_pressly_goose_v3/LICENSE) |
| github.com/riverqueue/river | v0.49.0 | [LICENSE](github.com_riverqueue_river/LICENSE) |
| github.com/riverqueue/river/riverdriver | v0.49.0 | [LICENSE](github.com_riverqueue_river_riverdriver/LICENSE) |
| github.com/riverqueue/river/riverdriver/riverpgxv5 | v0.49.0 | [LICENSE](github.com_riverqueue_river_riverdriver_riverpgxv5/LICENSE) |
| github.com/riverqueue/river/rivershared | v0.49.0 | [LICENSE](github.com_riverqueue_river_rivershared/LICENSE) |
| github.com/riverqueue/river/rivertype | v0.49.0 | [LICENSE](github.com_riverqueue_river_rivertype/LICENSE) |
| github.com/sethvargo/go-retry | v0.4.0 | [LICENSE](github.com_sethvargo_go-retry/LICENSE) |
| github.com/tidwall/gjson | v1.19.0 | [LICENSE](github.com_tidwall_gjson/LICENSE) |
| github.com/tidwall/match | v1.2.0 | [LICENSE](github.com_tidwall_match/LICENSE) |
| github.com/tidwall/pretty | v1.2.1 | [LICENSE](github.com_tidwall_pretty/LICENSE) |
| github.com/tidwall/sjson | v1.2.5 | [LICENSE](github.com_tidwall_sjson/LICENSE) |
| go.uber.org/multierr | v1.11.0 | [LICENSE.txt](go.uber.org_multierr/LICENSE.txt) |
| golang.org/x/crypto | v0.55.0 | [LICENSE](golang.org_x_crypto/LICENSE) |
| golang.org/x/sync | v0.23.0 | [LICENSE](golang.org_x_sync/LICENSE) |
| golang.org/x/sys | v0.47.0 | [LICENSE](golang.org_x_sys/LICENSE) |
| golang.org/x/text | v0.42.0 | [LICENSE](golang.org_x_text/LICENSE) |

The Go standard library is covered by [the Go license](go/LICENSE), retained from the official go1.27.0 source tag when the Nix toolchain omits GOROOT/LICENSE. Debian package notices (including FFmpeg) remain under `/usr/share/doc` in the application image.
