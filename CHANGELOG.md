# Changelog

## 0.1.0 (2026-10-11)

First release: an MCP server, a command-line tool and a Go client for Dockhand.

### Added

- 190 tools for everything a Dockhand manages; only the 73 that read are on unless writing and deleting are switched on
- see every Docker host and every container on it, find the ones that are not right, and read their logs
- start, stop, restart, make, change and remove containers, and look inside them: files, processes, what they use
- compose stacks: read, check, make, change, bring up, take down, move and remove, with each deploy's output kept
- stacks deployed from a git repository, with the credentials and repositories they are fetched with
- images, volumes and networks: list, inspect, pull, push, copy, export, remove and prune
- keep containers on current images: what has something newer, update one, and set or stop a schedule
- registries, notifications, schedules, tags, labels, icons, templates and config sets
- vulnerability scans: scan an image or a whole host, read the findings, export them
- users, the login, single sign-on, API tokens and agent tokens
- secret stores a stack's secrets are loaded from, so their values sit in no file
- Dockhand's own settings, whether a newer Dockhand is out, and its licence
- tools come in groups, so a client loads only the ones it needs
- anything that changes or deletes can first say what it would do, and the risky ones ask to be confirmed
- secrets are hidden in every answer unless asked for
- dates are answered in one form, UTC to the second
- slow work runs in the background and can be followed or cancelled
- works with a Dockhand whose login is on, with an API token, or off
- a Go client for every call of the Dockhand API, usable without the tools
