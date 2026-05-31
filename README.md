# arcctl

CLI for [Arc](https://github.com/Basekick-Labs/arc) — operator-facing client for Arc time-series databases.

> **Status:** v0.3.0-dev (PR3). Manages connection profiles, runs SQL queries, writes line protocol, and administers databases + measurements. `import` / `auth` / `cluster` subcommands ship in follow-up PRs.

## Why

Today operating Arc means hand-crafting `curl` calls: copying the bootstrap token from a stderr banner, building JSON query bodies, remembering header names like `x-arc-database`, and decoding `{"columns":[...],"data":[...]}` responses by eye. `arcctl` replaces that with a familiar CLI workflow modeled on `influx`, `kubectl`, and `clickhouse-client`.

## Install

Pre-built binaries + Docker images + Homebrew formula land in v1.0.

For now, build from source:

```bash
git clone https://github.com/Basekick-Labs/arcctl
cd arcctl
go build -o arcctl ./cmd/arcctl
./arcctl --version
```

Requires Go 1.25+.

## Quickstart

```bash
# 1. Create a connection profile from your Arc instance's bootstrap token
arcctl config create \
  --name local \
  --endpoint http://localhost:8000 \
  --token <token-from-arc-stderr-banner>

# 2. Confirm it's active (first connection auto-activates)
arcctl config current

# 3. List all profiles
arcctl config list
```

## Connection management

`arcctl` stores connection profiles in `~/.arcctl/config.toml` (mode 0600). One profile is marked active; commands use it by default.

```bash
# Add multiple environments
arcctl config create --name prod    --endpoint https://arc.prod.example.com --token PROD-TOK
arcctl config create --name staging --endpoint https://arc.staging.example.com --token STAGING-TOK --default-database metrics

# Switch active
arcctl config set-active staging

# Override per-command (later PRs)
arcctl --connection prod query "SELECT count(*) FROM cpu"

# Or via env vars (CI-friendly)
ARC_CONNECTION=prod arcctl query "..."
ARC_ENDPOINT=https://... ARC_TOKEN=... arcctl query "..."

# Show active (token redacted)
arcctl config current

# Remove
arcctl config delete staging --yes
```

### Precedence

1. `--connection NAME` flag
2. `--endpoint URL --token T` flags (full ad-hoc)
3. `ARC_CONNECTION` env var
4. `ARC_ENDPOINT` + `ARC_TOKEN` env vars (full ad-hoc)
5. Active connection in `~/.arcctl/config.toml`

If none are set, commands fail with a clear "no active connection" error.

### Config file location

Honors `ARCCTL_CONFIG` env var for test/CI overrides; otherwise `~/.arcctl/config.toml`.

## Querying

```bash
# Pretty table (default)
arcctl query "SELECT host, value FROM cpu ORDER BY value LIMIT 10"

# Override the database for one call
arcctl query --database metrics "SELECT count(*) FROM cpu"

# Read SQL from a file
arcctl query -f reports/p99.sql

# Pipe SQL from another command
echo "SELECT 1" | arcctl query

# Machine-parseable output
arcctl query "SELECT * FROM cpu" -o json | jq '.data[0]'
arcctl query "SELECT * FROM cpu" -o csv > out.csv

# Arrow IPC stream — feed it to pyarrow / duckdb / polars
arcctl query "SELECT * FROM cpu" -o arrow | duckdb -c "SELECT * FROM read_arrow('/dev/stdin')"
```

The output formats:

- `-o table` (default) — pretty-printed bordered table; honors `--no-header` and `--limit N`
- `-o json` — the raw `{"columns":[...],"data":[...]}` response, jq-friendly
- `-o csv` — RFC 4180 with a header row by default
- `-o arrow` — binary Arrow IPC stream on stdout; server-side execution time goes to stderr

## Writing

```bash
# Stdin pipe (most common in CI / log forwarders)
echo "cpu,host=server-1 value=42.5 $(date +%s)000000000" | arcctl write

# From a file
arcctl write -f payload.lp --database metrics --precision ms

# Explicit precision (default is nanoseconds, matching the server)
echo "cpu v=1 1700000000" | arcctl write --precision s
```

`--precision` accepts `ns`, `us`, `ms`, or `s` (anything else is rejected client-side before the request goes out). The body is streamed end-to-end — `cat huge.lp | arcctl write` never buffers the whole payload in memory.

## Database & measurement admin

```bash
# List every database the active token can see
arcctl db list

# Inspect one database (info + its measurements)
arcctl db show production

# Create an empty database (server validates name: alphanumeric + `_-`,
# max 64 chars, "system" / "internal" / "_internal" are reserved)
arcctl db create metrics

# Drop a database and ALL its files. Prompts for y/N; pass --yes to
# skip in scripts. The server requires delete.enabled=true in arc.toml
# AND an admin token — if either is missing the server's error message
# surfaces verbatim ("Set delete.enabled=true in arc.toml to enable.").
arcctl db drop old_metrics
arcctl db drop --yes ci_scratch          # no prompt

# List measurements inside a database (same data shown by `db show`,
# different default view)
arcctl measurement list --database metrics
arcctl measurement list -c prod --database logs -o json
```

`db list`, `db show`, and `measurement list` all support `-o table|json|csv` (no `-o arrow` — these endpoints return JSON, not Arrow IPC).

## TLS

For HTTPS endpoints, certificate verification is on by default. To skip verification (lab / self-signed certs only), use either:

- `--insecure` on a single command, or
- `insecure_tls = true` in the connection profile (set once via `arcctl config create --insecure`)

When verification is skipped, a `WARNING:` line is printed to stderr. The flag is a no-op on `http://` endpoints and the warning is suppressed.

## Roadmap

This repo is being built in [phased PRs](https://github.com/Basekick-Labs/arcctl/pulls):

- ~~**PR1** — scaffold, `config` subcommand tree, multi-connection store~~ ✅ shipped
- ~~**PR2** — `arcctl query`, `arcctl write`, output formats: table/json/csv/arrow~~ ✅ shipped
- ~~**PR3** — `arcctl db {list,show,create,drop}`, `arcctl measurement list`~~ ✅ shipped
- **PR4** — `arcctl import {csv,lp,parquet,msgpack}`
- **PR5** — `arcctl auth {token,whoami}`
- **PR6** — `arcctl cluster {status,nodes}`, `arcctl compaction`, `arcctl retention`
- **PR7** — release workflow + Homebrew tap + multi-arch Docker, cut v1.0.0

Target: arcctl 1.x speaks to Arc 26.06+.

## Development

```bash
go test -race ./...
go vet ./...
gofmt -l .
```

CI runs all three on every PR.

## License

Apache-2.0. See [LICENSE](LICENSE).
