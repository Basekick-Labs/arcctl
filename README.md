# arcctl

CLI for [Arc](https://github.com/Basekick-Labs/arc) — operator-facing client for Arc time-series databases.

> **Status:** v0.1.0-dev (PR1 scaffold). Manages connection profiles. `query` / `write` / `import` / `db` / `auth` / `cluster` subcommands ship in follow-up PRs.

## Why

Today operating Arc means hand-crafting `curl` calls: copying the bootstrap token from a stderr banner, building JSON query bodies, remembering header names like `x-arc-database`, and decoding column-major responses by eye. `arcctl` replaces that with a familiar CLI workflow modeled on `influx`, `kubectl`, and `clickhouse-client`.

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

## Roadmap

This repo is being built in [phased PRs](https://github.com/Basekick-Labs/arcctl/pulls):

- **PR1** (this) — scaffold, `config` subcommand tree, multi-connection store
- **PR2** — `arcctl query`, `arcctl write`
- **PR3** — `arcctl db {list,create,drop,show}`, `arcctl measurement list`
- **PR4** — `arcctl import {csv,lp,parquet,msgpack}`
- **PR5** — `arcctl auth {token,whoami}`
- **PR6** — `arcctl cluster {status,nodes}`, `arcctl compaction`, `arcctl retention`
- **PR7** — `-o csv` and `-o arrow` output formats
- **PR8** — release workflow + Homebrew tap + multi-arch Docker, cut v1.0.0

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
