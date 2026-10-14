# Configuration

Every option can be passed as a command-line flag, an environment variable, or a line in a configuration file.

| Variable | Flag | Description |
|---|---|---|
| `DOCKHAND_SERVER` | `--server`, `-s` | the address of the Dockhand web console, e.g. `http://dockhand:3000` |
| `DOCKHAND_TOKEN` | `--token`, `-t` | a Dockhand API token (web console, Profile, API tokens); leave it out when Dockhand's login is switched off |
| `DOCKHAND_ENABLE_WRITE` | `--enable-write` | load the tools that change something; without it every tool only reads |
| `DOCKHAND_ENABLE_DELETE` | `--enable-delete` | load the tools that remove something as well; needs `--enable-write` |
| `DOCKHAND_READ_ONLY` | `--read-only` | load only tools that read: the default, said out loud, and refused beside `--enable-write` |
| `DOCKHAND_TOOLSETS` | `--toolsets` | the groups of tools to load, `core,audit` unless set; see [toolsets.md](toolsets.md) |
| `DOCKHAND_ALLOW_TOOLS` | `--allow-tools` | load these tools too, by name or a pattern such as `container_*` |
| `DOCKHAND_DENY_TOOLS` | `--deny-tools` | never load these tools, by name or a pattern such as `*_stop` |
| `DOCKHAND_LOG` | | log level (`WARN` default; `DEBUG`, `TRACE`, ...) |
| `DOCKHAND_LISTEN` | `--listen` | serve over HTTP on this address (e.g. `:8080`) instead of stdio |
| `DOCKHAND_AUTH_TOKEN` | `--auth-token` | the token clients must send over HTTP; required with `--listen` |
| `DOCKHAND_ALLOW_NO_AUTH` | `--allow-no-auth` | serve HTTP with no token at all: anyone who can reach the port can use every tool |

A token can do whatever the user who made it can. When Dockhand's login is switched off, anyone who can reach it can do everything, with these tools or without them. `server_info` says when that is so.

## Configuration file

A `.dockhand-mcp` file in your home directory holds settings for every project. One in the current directory holds a project's own, which win. Keys are the variable names, with or without the `DOCKHAND_` prefix:

```env
SERVER=http://dockhand:3000
TOKEN=dh_...
ENABLE_WRITE=true
```

A flag or an environment variable beats either file.

## Run as a service (HTTP transport)

`serve --listen :8080` serves MCP over HTTP at `/mcp`, with `GET /healthz` beside it, instead of stdio.

Clients must send `Authorization: Bearer <token>` with the value of `DOCKHAND_AUTH_TOKEN`. The server will not start without one unless `DOCKHAND_ALLOW_NO_AUTH=true`.

Register it from any machine:

```bash
claude mcp add --transport http dockhand http://dockhand-mcp:8080/mcp --header "Authorization: Bearer $DOCKHAND_AUTH_TOKEN"
```

## Docker

Each release publishes an image for amd64 and arm64 to `ghcr.io/katbyte/dockhand-mcp`, tagged `vX.Y.Z`, `vX.Y` and `latest`.

`docker-compose.yml` runs it as an always-on service and reads its secrets from `.env`. Set `DOCKHAND_SERVER` and `TZ` in the compose file, then:

```bash
cp .env.example .env      # fill in DOCKHAND_TOKEN and DOCKHAND_AUTH_TOKEN
docker compose up -d
```

`make docker` builds the same image from source. It runs as a non-root user and has a health check.

`docker run --rm ghcr.io/katbyte/dockhand-mcp info` checks the connection, with the `DOCKHAND_*` variables passed by `-e`.
