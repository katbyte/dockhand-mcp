#!/usr/bin/env bash
# Move to the current Dockhand API document: pull the latest image, start a
# server once, read the document it serves about itself, and pin it under the
# server's own version. The document itself is not kept in git, only its pin
# (api-defs/get.sh). A document that changed is imported and generated, the
# API-level diff against the previous version is printed, and the previous
# definitions are removed (git holds them).
#
#   make spec-refresh
#   DOCKHAND_TEST_IMAGE=fnsys/dockhand:v1.0.51 .scripts/spec-refresh.sh   # one version rather than the latest
set -euo pipefail
cd "$(dirname "$0")/.."

svc=dockhand
pin="api-defs/${svc}-openapi.pin"

# current - the version, document and definitions the service is at now
current() { go run ./sdk/pandorest resolve -service "$svc" 2>/dev/null | cut -f2-; }

IFS=$'\t' read -r have_version have_spec have_defs <<< "$(current)"
had="$(sed -n 's/^sha256=//p' "$pin" 2>/dev/null || true)"

api-defs/get.sh --repin "${DOCKHAND_TEST_IMAGE:-fnsys/dockhand:latest}"
version="$(sed -n 's/^version=//p' "$pin")"

if [ "$had" = "$(sed -n 's/^sha256=//p' "$pin")" ]; then
  echo "$svc: $version is current (the document is unchanged)"
  exit 0
fi

new_spec="api-defs/${svc}-openapi-${version}.json"
if [ "$new_spec" = "${have_spec:-}" ]; then
  echo "$svc: $version changed under the same version; pinned anew"
elif [ -n "${have_spec:-}" ]; then
  # the older document is only a fetched copy, and the newest on disk is the one generated from
  rm -f "$have_spec"
fi

make --no-print-directory generate

IFS=$'\t' read -r _ _ new_defs <<< "$(current)"
if [ -n "${have_defs:-}" ] && [ "$have_defs" != "$new_defs" ]; then
  echo
  echo "==> $svc: $have_version -> $version"
  go run ./sdk/pandorest diff -old "$have_defs" -new "$new_defs" || true
  git rm -q -r "$have_defs"
  echo "removed $have_defs (git holds it)"
elif [ -n "${have_defs:-}" ]; then
  echo
  echo "==> $svc: $version, changed in place; git diff shows what"
fi

echo
echo "review the diff, run make test and make testacc, and commit; a workaround whose bug the new document fixes fails the import and names itself"
