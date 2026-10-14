# dockhand-mcp - a Dockhand MCP server, CLI and Go SDK

[![GitHub release](https://img.shields.io/github/v/release/katbyte/dockhand-mcp?color=blueviolet)](https://github.com/katbyte/dockhand-mcp/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/katbyte/dockhand-mcp?color=00ADD8)](https://github.com/katbyte/dockhand-mcp/blob/main/go.mod)
[![License](https://img.shields.io/github/license/katbyte/dockhand-mcp?color=blue)](https://github.com/katbyte/dockhand-mcp/blob/main/LICENSE)
![build](https://github.com/katbyte/dockhand-mcp/actions/workflows/build.yaml/badge.svg)
![tests](https://github.com/katbyte/dockhand-mcp/actions/workflows/pr-acceptance.yaml/badge.svg)
![lint](https://github.com/katbyte/dockhand-mcp/actions/workflows/pr-golangci-lint.yaml/badge.svg)
[![coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/katbyte/dockhand-mcp/badges/coverage.json)](https://github.com/katbyte/dockhand-mcp/actions/workflows/coverage.yaml)

Lets a model see and look after every container on every Docker host a [Dockhand](https://dockhand.pro) manages, from any [MCP](https://modelcontextprotocol.io) client.

**Detection is code, correction is judgment.** Audits find what is wrong by fixed rules, the same way every time. Nothing is changed until you switch changes on, and a change can be previewed first.

## Install

```bash
go install github.com/katbyte/dockhand-mcp@latest
```

## Quick start

Register it with Claude Code:

```bash
claude mcp add dockhand -e DOCKHAND_SERVER=http://dockhand:3000 -e DOCKHAND_TOKEN=... -- dockhand-mcp serve
```

Then ask "is anything unhealthy anywhere?". Leave the token out when Dockhand's login is off.

`dockhand-mcp info` checks the connection from a shell. Every option, the config file, serving over HTTP and the Docker image are in [docs/configuration.md](docs/configuration.md).

## What is in the box

- **Every host at once.** A question goes to every host unless one is named.
- **Audits.** Thirteen tools look across every host for what needs attention.
- **Secrets stay hidden.** Passwords, tokens and keys are hidden unless a call asks for them.
- **Reads until told otherwise.** Nothing is changed without `--enable-write`, or removed without `--enable-delete` as well.
- **A Go SDK.** `sdk/dockhand` has a method for every call in the Dockhand API. See [docs/sdk.md](docs/sdk.md).

There are 225 tools. They cover hosts, containers, stacks, images, volumes and networks, and Dockhand's own registries, schedules, backups, users and settings. Written against Dockhand 1.0.51.

## Audits

The audits load by default and only read. Start with `audit_all`, then call the audit whose count is not zero. Each name links to its full rules.

| Audit | What it finds, and what fixes it |
|---|---|
| [`audit_all`](docs/audits.md#audit_all) | A count for every audit, over every host. Start here. |
| [`audit_health`](docs/audits.md#audit_health) | Containers broken now: unhealthy, restarting, exited with an error. `container_logs` says why; `container_restart` starts one again. |
| [`audit_hosts`](docs/audits.md#audit_hosts) | Docker hosts Dockhand cannot reach. `env_test` says why. |
| [`audit_stacks`](docs/audits.md#audit_stacks) | Compose stacks partly running or not running. `stack_start` or `stack_deploy` brings one up. |
| [`audit_healthcheck`](docs/audits.md#audit_healthcheck) | Running containers nothing checks. Add a health check to the compose file with `stack_edit`. |
| [`audit_restart_policy`](docs/audits.md#audit_restart_policy) | Running containers that would stay down after a reboot. `container_limits` sets a restart policy. |
| [`audit_updates`](docs/audits.md#audit_updates) | Containers with a newer image out. `container_update` updates one. |
| [`audit_vulnerabilities`](docs/audits.md#audit_vulnerabilities) | Images with critical or high known vulnerabilities, or never scanned. `image_scan_all` scans a host. |
| [`audit_security`](docs/audits.md#audit_security) | Containers given more of their host than they need. Each is a decision; change the compose file with `stack_edit`. |
| [`audit_image_tags`](docs/audits.md#audit_image_tags) | Containers on `latest` or no tag. Name a version in the compose file with `stack_edit`. |
| [`audit_limits`](docs/audits.md#audit_limits) | Running containers with no memory limit. `container_limits` sets one. |
| [`audit_stopped`](docs/audits.md#audit_stopped) | Containers left stopped for a week or more. `container_delete` or `container_prune` removes them. |
| [`audit_unused`](docs/audits.md#audit_unused) | Images, volumes and networks nothing uses. `image_prune`, `volume_prune` and `network_prune` remove them. |

## Changing things

Out of the box every tool only reads.

- `--enable-write` loads the tools that change something. `--enable-delete` loads the ones that remove something as well.
- A change takes `dry_run` and says what it would do. A removal removes nothing until called with `confirm`.
- Some changes are bigger than they look: changing a container makes it again. Read [docs/changing-things.md](docs/changing-things.md) first.

## Tools

The default is `core` and `audit`: 21 read-only tools, about 6,000 tokens of a client's context. `--toolsets` loads more groups, up to `all`.

```sh
dockhand-mcp tools --toolsets all --enable-write --enable-delete   # print every tool with what it does
```

- [docs/tools.md](docs/tools.md): every tool, by what it acts on.
- [docs/toolsets.md](docs/toolsets.md): the groups, what each costs, and adding or leaving out single tools.

## How this is built

- **Every tool has a live test.** Each one must answer against a real Dockhand in Docker, which manages real Docker hosts. CI fails otherwise.
- **The Go client covers the whole API.** CI fails if a call has no method. Its calls run against the same real server, except 26 of the 395, mostly ones that need a licence or a signed-in browser session.
- **CI also runs** the unit tests, golangci-lint with gosec, govulncheck, CodeQL, a Trivy scan of the built binary and a check of the vendored dependencies.
- **Everything else is linted too:** the workflows (actionlint, zizmor, yamllint), shell scripts, markdown and spelling.

## More

- [docs/development.md](docs/development.md): building and testing.
- [CONTRIBUTING.md](CONTRIBUTING.md): how releases are cut.
- [CHANGELOG.md](CHANGELOG.md): what changed in each version.
- [api-defs/README.md](api-defs/README.md): what Dockhand's API does that it does not document.
