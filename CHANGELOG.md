# Changelog

## 0.1.0 (2026-10-12)

First release: an MCP server, a command-line tool and a Go client for Dockhand.

### Added

- 203 tools for everything a Dockhand manages; the 21 that show what runs where and audit it load by default, and nothing that writes or deletes is on until switched on
- audits that look for problems across every host: what is broken now, what nothing checks, what would not come back after a reboot, what has updates or known vulnerabilities, what was given too much of its host, and what is left over; one call counts them all
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
