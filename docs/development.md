# Development

```bash
make            # fmt + build
make check-all  # build + unit tests + both live suites (needs docker) + every linter
make generate   # import Dockhand's description of its API and regenerate sdk/dockhand
```

## Tests

`make test` is fast and needs no server. The first run fetches Dockhand's description of its API, which needs Docker that once.

Everything else runs against **a real Dockhand in Docker**, because a stub can only confirm what you already believed.

`.scripts/testenv.sh` starts a fresh Dockhand with two Docker hosts of its own to manage, one behind a real Hawser agent. It also starts a registry, a secret store, a git server and a backup server for Dockhand to use. Nothing is pointed at the Docker of the machine the tests run on.

| | Covers | Command |
|---|---|---|
| `acceptance/dockhand/` | the client: every read the API has, and each kind of thing made, changed and removed | `make testacc-sdk` |
| `acceptance/tools/` | every tool, journeys that chain them the way a session does, and the built binary | `make testacc-tools` |

```bash
make testacc        # both, each on its own throwaway server and hosts, torn down after
make cover          # all three suites, merged into one coverage number
```

Each suite runs twice, because a Dockhand is run two ways: with no login, as it comes, and with its login on.

A run of the tool suite fails when a registered tool was never seen to answer in it.

## Releases

See [CONTRIBUTING.md](../CONTRIBUTING.md).
