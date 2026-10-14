# Contributing

Building and testing are covered in [docs/development.md](docs/development.md). `make check-all` runs everything CI runs, and needs Docker.

## Releases

- Changes collect under "Unreleased" in [CHANGELOG.md](CHANGELOG.md).
- A version is tagged when a batch is worth upgrading for, not for every change.
- Breaking changes are grouped into one release rather than shipped one per release.
- Pushing a `vX.Y.Z` tag publishes the release: binaries, the Docker image and the Homebrew formula.
