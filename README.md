# dockhand-mcp - a Dockhand MCP server, CLI and Go SDK

[![GitHub release](https://img.shields.io/github/v/release/katbyte/dockhand-mcp?color=blueviolet)](https://github.com/katbyte/dockhand-mcp/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/katbyte/dockhand-mcp?color=00ADD8)](https://github.com/katbyte/dockhand-mcp/blob/main/go.mod)
[![License](https://img.shields.io/github/license/katbyte/dockhand-mcp?color=blue)](https://github.com/katbyte/dockhand-mcp/blob/main/LICENSE)
![build](https://github.com/katbyte/dockhand-mcp/actions/workflows/build.yaml/badge.svg)
![tests](https://github.com/katbyte/dockhand-mcp/actions/workflows/pr-acceptance.yaml/badge.svg)
![lint](https://github.com/katbyte/dockhand-mcp/actions/workflows/pr-golangci-lint.yaml/badge.svg)
[![coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/katbyte/dockhand-mcp/badges/coverage.json)](https://github.com/katbyte/dockhand-mcp/actions/workflows/coverage.yaml)

An [MCP](https://modelcontextprotocol.io) server, CLI and Go SDK for [Dockhand](https://dockhand.pro): **see every container on every Docker host a Dockhand manages, find the ones that are not right, read their logs and restart them** - from Claude Code, Claude Desktop, or any other MCP client.

A Dockhand already knows every host it manages. dockhand-mcp asks it, so a model needs no login on each machine and need not know which host a container is on. "Is anything unhealthy anywhere?" is one call. "Why does it keep restarting?" is two more.

### What is in the box

- **Every host at once.** A question goes to every host unless one is named, and a host that could not be reached is named beside the ones that answered.
- **Audits.** Thirteen tools that look across every host for what needs attention: containers that are broken, hosts out of reach, images with known vulnerabilities, things nothing uses.
- **Secrets stay hidden.** Passwords, tokens and keys in a container's variables, command line and logs are hidden unless a call asks for them.
- **Reads until told otherwise.** Nothing is changed without `--enable-write`, and nothing is removed without `--enable-delete` as well.
- **A Go SDK.** `sdk/dockhand` has a method for every call in the Dockhand API, 395 of them, and knows nothing of MCP.
- **Tested against a real server.** Every tool runs against a real Dockhand in Docker that manages real Docker hosts, with its login off and with it on.

There are 203 tools in all: hosts, containers, compose stacks, images, volumes and networks, and Dockhand's own side, its registries, schedules, notifications, scans, users and settings. Its backups are in the SDK and have no tools yet.

## Installation

```bash
go install github.com/katbyte/dockhand-mcp@latest
```

Written against Dockhand 1.0.51.

## Configuration

All options can be passed as command-line flags, environment variables, or via a configuration file.

| Variable | Flag | Description |
|---|---|---|
| `DOCKHAND_SERVER` | `--server`, `-s` | the address of the Dockhand web console, e.g. `http://dockhand:3000` |
| `DOCKHAND_TOKEN` | `--token`, `-t` | a Dockhand API token (web console, Profile, API tokens); leave it out when Dockhand's login is switched off |
| `DOCKHAND_ENABLE_WRITE` | `--enable-write` | load the tools that change something; without it every tool only reads |
| `DOCKHAND_ENABLE_DELETE` | `--enable-delete` | load the tools that remove something as well; needs `--enable-write` |
| `DOCKHAND_READ_ONLY` | `--read-only` | load only tools that read: the default, said out loud, and refused beside `--enable-write` |
| `DOCKHAND_TOOLSETS` | `--toolsets` | the groups of tools to load, `core,audit` unless set; see [Choosing which tools load](#choosing-which-tools-load) |
| `DOCKHAND_ALLOW_TOOLS` | `--allow-tools` | load these tools too, by name or a pattern such as `container_*` |
| `DOCKHAND_DENY_TOOLS` | `--deny-tools` | never load these tools, by name or a pattern such as `*_stop` |
| `DOCKHAND_LOG` | | log level (`WARN` default; `DEBUG`, `TRACE`, ...) |
| `DOCKHAND_LISTEN` | `--listen` | serve over HTTP on this address (e.g. `:8080`) instead of stdio |
| `DOCKHAND_AUTH_TOKEN` | `--auth-token` | the token clients must send over HTTP; required with `--listen` |
| `DOCKHAND_ALLOW_NO_AUTH` | `--allow-no-auth` | serve HTTP with no token at all: anyone who can reach the port can use every tool |

A token can do whatever the user who made it can. When Dockhand's login is switched off, anyone who can reach it can do everything, with these tools or without them, and `server_info` says when that is so.

### Configuration File

A `.dockhand-mcp` file in your home directory holds settings for every project, and one in the current directory holds a project's own, which win. Keys are the variable names, with or without the `DOCKHAND_` prefix:

```env
SERVER=http://dockhand:3000
TOKEN=dh_...
ENABLE_WRITE=true
```

A flag or an environment variable beats either file.

## Usage

Quick connectivity check:

```bash
dockhand-mcp info
```

### Register with Claude Code

`.mcp.json`:

```json
{
  "mcpServers": {
    "dockhand": {
      "command": "dockhand-mcp",
      "args": ["serve"],
      "env": {
        "DOCKHAND_SERVER": "http://dockhand:3000",
        "DOCKHAND_TOKEN": "..."
      }
    }
  }
}
```

Or from the shell:

```bash
claude mcp add dockhand -e DOCKHAND_SERVER=http://dockhand:3000 -e DOCKHAND_TOKEN=... -- dockhand-mcp serve
```

### Run as a service (HTTP transport)

`serve --listen :8080` serves MCP over HTTP at `/mcp`, with `GET /healthz` beside it, instead of stdio. Clients must send `Authorization: Bearer <token>` with the value of `DOCKHAND_AUTH_TOKEN`, and the server will not start without one unless `DOCKHAND_ALLOW_NO_AUTH=true`. Register it from any machine:

```bash
claude mcp add --transport http dockhand http://dockhand-mcp:8080/mcp --header "Authorization: Bearer $DOCKHAND_AUTH_TOKEN"
```

### Docker

Each release publishes an image for amd64 and arm64 to `ghcr.io/katbyte/dockhand-mcp`, tagged `vX.Y.Z`, `vX.Y` and `latest`. `docker-compose.yml` runs it as an always-on service and reads its secrets from `.env`. Set `DOCKHAND_SERVER` and `TZ` in the compose file, then:

```bash
cp .env.example .env      # fill in DOCKHAND_TOKEN and DOCKHAND_AUTH_TOKEN
docker compose up -d
```

`make docker` builds the same image from source. It runs as a non-root user and has a health check. `docker run --rm ghcr.io/katbyte/dockhand-mcp info` checks the connection, with the `DOCKHAND_*` variables passed by `-e`.

## MCP Tools

Tools are named for what they act on. A tool about a Docker host takes `env`, the host's name or number as `env_list` gives it; left out, every host is asked. `dockhand-mcp tools --toolsets all --enable-write --enable-delete` prints every tool with what it does.

| Resource | Tools |
|---|---|
| audits | `audit_all` (every audit over every host, counts only: start here), `audit_health` (containers broken right now), `audit_hosts` (hosts out of reach), `audit_stacks` (stacks not wholly up), `audit_healthcheck` (running containers nothing checks), `audit_restart_policy` (would stay down after a reboot), `audit_updates` (a newer image is out), `audit_vulnerabilities` (images with critical or high vulnerabilities, or never scanned), `audit_security` (containers given more of their host than they need), `audit_image_tags` (on `latest` or no tag), `audit_limits` (no memory limit), `audit_stopped` (left stopped for days), `audit_unused` (images, volumes and networks nothing uses) |
| server | `server_info` (connection, version, whether the login is on), `server_update_check`, `server_about`, and for Dockhand's own disk, where the compose files of its stacks are, `server_files`, `server_read`, `server_mkdir` |
| hosts (environments) | `env_list`, `env_get`, `env_test`, `env_create`, `env_edit`, `env_schedules` (what Dockhand does for a host by itself, and when), `env_delete` (forget a host, leaving what runs on it), `env_disk` (what fills its disk) |
| agents | `agent_list`, `agent_token_create`, `agent_token_delete` (the Hawser agents that call in to Dockhand, and their tokens) |
| containers | `container_list`, `container_get`, `container_logs`, `container_start`, `container_stop`, `container_restart`, `container_stats`, `container_sizes`, `container_top` (its processes), `container_compose` (one written out as a compose file), `container_events` (what containers did and when), `container_create`, `container_edit`, `container_limits`, `container_pause`, `container_unpause`, `container_rename`, `container_exec`, `container_delete`, `container_prune`, `container_events_clear` |
| files in a container | `container_files`, `container_read`, `container_file_download`, `container_file_write`, `container_file_upload`, `container_file_mkdir`, `container_file_move`, `container_file_perms`, `container_file_delete` |
| updates | `container_updates` (which have a newer image out), `container_update`, `container_updates_clear`, `container_autoupdate`, `container_autoupdate_set` (which containers Dockhand updates by itself) |
| stacks | `stack_list`, `stack_get`, `stack_logs`, `stack_start`, `stack_stop`, `stack_restart`, `stack_validate`, `stack_deploys` (each time one was brought up), `stack_scan` (look for compose files to adopt), `stack_create`, `stack_edit`, `stack_deploy`, `stack_down` (remove its containers, keep its file), `stack_adopt` (take on a stack someone else brought up), `stack_move`, `stack_delete`, `stack_deploys_delete` |
| images | `image_list`, `image_get`, `image_pull`, `image_tag`, `image_delete`, `image_prune`, `image_export`, `image_load` (to and from a file on this machine) |
| volumes | `volume_list`, `volume_get`, `volume_files`, `volume_read`, `volume_create`, `volume_clone`, `volume_delete`, `volume_prune`, `volume_export` |
| networks | `network_list`, `network_get`, `network_create`, `network_connect`, `network_disconnect`, `network_delete`, `network_prune` |
| registries | `registry_list`, `registry_test`, `registry_images`, `registry_tags`, `registry_create`, `registry_edit`, `registry_delete`, `registry_image_delete`, `image_push` |
| schedules | `schedule_list` (everything Dockhand does by itself), `schedule_history` (how each run went), `schedule_run`, `schedule_set` (switch one off or on), `schedule_delete`, `schedule_history_delete` |
| notifications | `notification_list`, `notification_test`, `notification_create`, `notification_edit`, `notification_link` (tie a channel to a host), `notification_link_delete`, `notification_delete` |
| vulnerabilities | `vulnerability_list`, `vulnerability_export`, `image_scan`, `image_scan_all`, `image_scan_get`, `scanner_get`, `scanner_set`, `scanner_delete`, `scanner_cache_clear` |
| users and login | `user_list`, `user_create`, `user_edit`, `user_delete`, `login_get`, `login_set`, `token_list`, `sso_list`, `sso_create`, `sso_edit`, `sso_delete` |
| settings | `settings_get`, `settings_set`, `preferences_get`, `preferences_set` (what the web interface remembers), `favorite_list`, `favorite_set` (the containers kept at hand in a log view) |
| secret stores | `secret_provider_list`, `secret_provider_test`, `secret_provider_keys` (names only, never values), `secret_provider_create`, `secret_provider_edit`, `secret_provider_delete` |
| git | `git_credential_list`, `git_credential_create`, `git_credential_edit`, `git_credential_delete`, `git_repository_list`, `git_repository_test`, `git_repository_variables`, `git_repository_create`, `git_repository_edit`, `git_repository_delete`, `git_stack_list`, `git_stack_get`, `git_stack_create`, `git_stack_edit`, `git_stack_sync`, `git_stack_deploy`, `git_stack_delete` |
| templates | `template_list`, `template_get`, `template_source_list`, `template_source_create`, `template_source_edit`, `template_source_delete` |
| config sets | `config_set_list`, `config_set_create`, `config_set_edit`, `config_set_delete` (bundles of container settings to hand to `container_create`) |
| tags, labels and icons | `tag_list`, `tag_create`, `tag_edit`, `tag_assign`, `tag_delete`, `label_list`, `label_edit`, `label_assign`, `label_delete` (labels are for hosts), `icon_list`, `icon_search`, `icon_set`, `icon_download`, `icon_delete` |
| jobs | `job_get`, `job_cancel` (slow work left running) |

### Before changing anything

- **Changes are off until switched on.** Tools that change something load only with `--enable-write`, and tools that remove something only with `--enable-delete` as well. A change takes `dry_run` and says what it would do (`login_set` asks for `confirm` instead); a removal says what would go and removes nothing until called with `confirm`.
- **Changing a container makes it again.** `container_edit` and `container_update` remove the container and make it again under the same name with the change, so anything it wrote outside its volumes is lost. `container_limits` is the exception: memory, processors and the restart policy are set as it runs.
- **A prune lists what it would take.** `volume_prune` removes named volumes too, which `docker volume prune` leaves. `network_prune` would take the networks of a stack that is only stopped, which then will not start, so it refuses unless `stranding` is given.
- **Removing a stack removes its containers.** `stack_down` is the gentler one: the containers go, the compose file stays, and `stack_deploy` brings it back.
- **Looking inside a volume needs `--enable-write`.** `volume_files` and `volume_read` change nothing, but Dockhand has the host start a small helper container to look, which a session told only to read should not be doing.
- **Automatic updates restart things with nobody watching.** `container_autoupdate_set` and a host's `auto_update` schedule pull newer images and make containers again on their own.
- **Slow work does not hold a session up.** A pull, a deploy or a prune still going after 50 seconds answers with a job id, and `job_get` says how it went.

Dockhand saves a good deal without checking it, and some of what it does is a surprise. The tools check first and say so in their own descriptions; what Dockhand does that it does not document is written up in [api-defs/README.md](api-defs/README.md).

### Choosing which tools load

**The default is `core` and `audit`: 21 read-only tools, about 6,000 tokens.** Every tool's description takes room in a client's context before anyone asks a question, so `--toolsets` / `DOCKHAND_TOOLSETS` loads only the groups a session needs. `core` always comes along, because nothing else can say which hosts there are.

| toolset | what it is for | tools | with core | ~tokens |
|---|---|---|---|---|
| `core` *(default)* | what runs where: hosts, containers, stacks and their logs | 8 | 8 | 2,600 |
| `audit` *(default)* | looking for what is wrong | 13 | 21 | 6,000 |
| `manage` | starting, stopping and restarting | 7 | 15 | 4,100 |
| `containers` | making, changing and looking into a container | 15 | 23 | 8,300 |
| `files` | the files inside a container | 9 | 17 | 5,900 |
| `updates` | keeping containers on current images | 5 | 13 | 4,500 |
| `stacks` | compose stacks: making, changing, deploying, removing | 15 | 23 | 8,700 |
| `resources` | images, volumes and networks | 25 | 33 | 10,900 |
| `setup` | adding a host, its agents and its schedules | 9 | 17 | 6,800 |
| `schedules` | what Dockhand does by itself and how each run went | 6 | 14 | 4,700 |
| `registries` | the registries Dockhand keeps, what is in them, pushing to one | 9 | 17 | 5,500 |
| `notifications` | who Dockhand tells of what | 7 | 15 | 5,800 |
| `vulnerabilities` | scanning images and reading what was found | 9 | 17 | 6,000 |
| `users` | who may sign in: users, the login, single sign-on | 11 | 19 | 6,200 |
| `settings` | Dockhand's own settings and what its web interface remembers | 8 | 16 | 5,800 |
| `secrets` | the secret stores a stack's secrets are loaded from | 6 | 14 | 4,700 |
| `git` | stacks deployed from git, with their repositories and credentials | 17 | 25 | 9,600 |
| `templates` | app templates and config sets | 10 | 18 | 6,000 |
| `tags` | tags, host labels and icons | 14 | 22 | 7,300 |
| `all` | everything | 203 | 203 | 71,900 |

Tokens are what the model sees: each tool's name, description and inputs, measured over a real `tools/list` at four bytes a token.

`--toolsets` also takes a family, which is every tool whose name starts with it: `container`, `stack`, `image`, `volume`, `network`, `env` and so on.

```sh
DOCKHAND_TOOLSETS=all DOCKHAND_ENABLE_WRITE=true DOCKHAND_ENABLE_DELETE=true   # every tool
DOCKHAND_TOOLSETS=manage DOCKHAND_ENABLE_WRITE=true                            # core, and starting, stopping and restarting
DOCKHAND_TOOLSETS=stacks,git DOCKHAND_ENABLE_WRITE=true                        # core, and compose stacks, from a file or from git
DOCKHAND_TOOLSETS=container                                                    # core, and every container tool that only reads
```

`dockhand-mcp tools` prints what the current flags would load, grouped by toolset, and needs no server:

```sh
dockhand-mcp tools                               # the default set
dockhand-mcp tools --toolsets all --enable-write # every tool but the ones that remove
dockhand-mcp tools -q                            # names only
```

### One tool more, or a few less

`--allow-tools` adds tools by name to whatever the toolsets load; with no toolset given, it loads only the tools it names. `--deny-tools` takes tools away. Both take names and patterns with a leading or trailing `*`, and `--allow-tools` also takes `essential`: `server_info`, `env_list`, `container_list` and `container_logs`. Neither gets a tool that changes something past `--enable-write`.

```sh
DOCKHAND_ALLOW_TOOLS=essential                                                             # the four essential tools and nothing else
DOCKHAND_ENABLE_WRITE=true DOCKHAND_TOOLSETS=core DOCKHAND_ALLOW_TOOLS=container_restart   # core, and restarting a container as well
DOCKHAND_ENABLE_WRITE=true DOCKHAND_TOOLSETS=manage DOCKHAND_DENY_TOOLS=*_stop             # start and restart, never stop
```

### Finding what is not right

The audits look for it. They load by default and only read.

```text
audit_all                             every audit over every host, counted: where to look
audit_health                          what is broken now, each with why: the health check's words, the exit code
audit_healthcheck                     what could never be found unhealthy, because nothing checks it
audit_security env=<host>             what was given more of its host than a container needs
```

And one container looked into, with `core`:

```text
container_list problems=true          every container that is unhealthy, restarting or exited with an error, on every host
container_get container=<name>        why: what its health check printed, its exit code, whether the host killed it for memory
container_logs container=<name>       what it wrote before it stopped
stack_logs stack=<name>               what its neighbours wrote around the same time
container_restart container=<name>    once the cause is dealt with
```

## Using the client on its own

`sdk/dockhand` is a plain Go client for the Dockhand API that knows nothing of MCP. If you only want to talk to Dockhand from Go, take it and ignore the rest:

```go
import "github.com/katbyte/dockhand-mcp/sdk/dockhand"

client, err := dockhand.New("http://dockhand:3000", os.Getenv("DOCKHAND_TOKEN"))
environments, err := client.GetEnvironments(ctx)
containers, err := client.GetContainers(ctx, dockhand.GetContainersOptions{Env: &environments.Answer[0].Id})
```

- **Every call Dockhand has.** A method for each of the 395, named for the call: `GET /api/stacks/{name}/compose` is `GetStacksByNameCompose`. `make apicheck` fails if one is missing.
- **With a login or without.** `dockhand.NewWithoutLogin` is for a Dockhand with its login switched off.
- **A read is tried again, a change is not.** A read is asked up to three times when the server cannot be reached or the connection drops; a change is sent once.
- **Quiet unless asked.** It logs nothing unless given a logger with `client.WithLog`, and then passwords, tokens and keys are blanked. `DOCKHAND_LOG=trace` shows the traffic.
- **Errors say what Dockhand said.** A status a call does not document is a `*client.StatusError`; `client.IsNotFound`, `IsUnauthorized` and `IsForbidden` read the common ones.

The client is generated by [pandorest](https://github.com/katbyte/pandorest) from the description of its API that every Dockhand serves at `/openapi.json`; nothing is taken from Dockhand's source. That description is wrong or silent in places, and each fix is proved against a running server and written up in [api-defs/README.md](api-defs/README.md).

## Development

```bash
make            # fmt + build
make check-all  # build + unit tests + both live suites (needs docker) + every linter
make generate   # import Dockhand's description of its API and regenerate sdk/dockhand
```

### Tests

`make test` is fast and needs no server. The first run fetches Dockhand's description of its API, which needs Docker that once.

Everything else runs against **a real Dockhand in Docker**, because a stub can only confirm what you already believed. `.scripts/testenv.sh` starts a fresh Dockhand with two Docker hosts of its own to manage, one behind a real Hawser agent, and a registry, a secret store and a git server for it to use. Nothing is pointed at the Docker of the machine the tests run on.

| | Covers | Command |
|---|---|---|
| `acceptance/dockhand/` | the client: every read the API has, and each kind of thing made, changed and removed | `make testacc-sdk` |
| `acceptance/tools/` | every tool, journeys that chain them the way a session does, and the built binary | `make testacc-tools` |

```bash
make testacc        # both, each on its own throwaway server and hosts, torn down after
make cover          # all three suites, merged into one coverage number
```

Each suite runs twice, because a Dockhand is run two ways: with no login, as it comes, and with its login on. A run fails when a registered tool was never seen to answer in it.
