#!/usr/bin/env bash
# Fetch the Dockhand API document the client is generated from, from Dockhand itself. Every Dockhand server serves a description of its own API at
# /openapi.json, to anyone who asks, and that is the one source: this starts a throwaway server of the published image pinned in dockhand-openapi.pin,
# asks it for /openapi.json, and removes it again. Nothing is taken from Dockhand's source repository, here or anywhere else in this repository.
#
# The document is Dockhand's own, so it is not committed: git ignores it, and what is committed is the pin, the definitions imported from the document
# and the client generated from those. What is fetched is held to the checksum in the pin, so what is generated from is known to be what was reviewed.
#
#   api-defs/get.sh                  # make sure the pinned document is on disk: fetch it when it is missing or is not the one pinned
#   api-defs/get.sh --repin [image]  # fetch the document of an image (default fnsys/dockhand:latest) and pin that one
#
# Needs docker, curl and jq. make runs the first form before anything that reads the document.
set -euo pipefail
cd "$(dirname "$0")/.."

svc=dockhand
pin="api-defs/${svc}-openapi.pin"

# checksum is the sha256 of a document's content, however it is laid out: keys sorted, no spacing
checksum() {
  if command -v sha256sum >/dev/null 2>&1; then
    jq -cS . "$1" | sha256sum | cut -d' ' -f1
  else
    jq -cS . "$1" | shasum -a 256 | cut -d' ' -f1
  fi
}

# fetch writes the document a fresh server of an image serves to a file
fetch() {
  echo "==> fetching the API document from a throwaway $1..." >&2
  DOCKHAND_TEST_IMAGE="$1" ./.scripts/testenv.sh spec > "$2"
  jq -e '.info.version and .paths' "$2" >/dev/null || { echo "get.sh: $1 did not serve an API document" >&2; exit 1; }
}

fetched="$(mktemp)"
trap 'rm -f "$fetched"' EXIT

if [ "${1:-}" = "--repin" ]; then
  image="${2:-fnsys/dockhand:latest}"
  fetch "$image" "$fetched"
  version="$(jq -r .info.version "$fetched")"
  sha256="$(checksum "$fetched")"
  cp "$fetched" "api-defs/${svc}-openapi-${version}.json"
  printf '%s\n' "# The Dockhand API document sdk/dockhand is generated from: its version, the published image whose server serves it at /openapi.json, and the sha256 of its content." "# Written by api-defs/get.sh --repin, read by api-defs/get.sh. The document is fetched from a running server of that image and is not kept in this repository." "version=${version}" "image=fnsys/dockhand:v${version}" "sha256=${sha256}" > "$pin"
  echo "$svc: pinned ${version} (${sha256})"
  exit 0
fi

[ -f "$pin" ] || { echo "get.sh: $pin is missing; run api-defs/get.sh --repin" >&2; exit 1; }
version="$(sed -n 's/^version=//p' "$pin")"
image="$(sed -n 's/^image=//p' "$pin")"
sha256="$(sed -n 's/^sha256=//p' "$pin")"
doc="api-defs/${svc}-openapi-${version}.json"

if [ -f "$doc" ] && [ "$(checksum "$doc")" = "$sha256" ]; then
  exit 0
fi

fetch "$image" "$fetched"
got="$(checksum "$fetched")"
if [ "$got" != "$sha256" ]; then
  echo "get.sh: the document $image serves is not the one pinned" >&2
  echo "  pinned:  $sha256" >&2
  echo "  fetched: $got" >&2
  echo "the image changed under its tag, or the pin is stale; api-defs/get.sh --repin $image pins what it serves now, and make generate shows what that changes" >&2
  exit 1
fi
cp "$fetched" "$doc"
echo "$svc: fetched $doc"
