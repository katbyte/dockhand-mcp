#!/usr/bin/env bash
#
# Bring up a throwaway Dockhand in Docker with Docker hosts of its own to
# manage, and print the environment the live test suites need.
#
#   eval "$(.scripts/testenv.sh up)"   # the server, two Docker hosts and an agent
#   .scripts/testenv.sh down           # stop and remove everything this script started
#   .scripts/testenv.sh logs           # what each container wrote
#   .scripts/testenv.sh spec           # the API document a fresh server serves, on stdout
#
# Dockhand manages Docker hosts, so a Dockhand with nothing to manage tests
# nothing, and one pointed at this machine's own Docker would have the suites
# stopping and deleting whatever else runs here. So each host is a Docker of
# its own inside a container (Docker in Docker), which nothing but the suites
# ever sees:
#
#   <prefix>               the Dockhand server, fresh: no login, no environments
#   <prefix>-docker-alpha  a Docker host Dockhand reaches through its socket, shared on a volume
#   <prefix>-docker-beta   a second Docker host, which Dockhand cannot reach by itself
#   <prefix>-agent-beta    a Hawser agent in front of beta, which is how Dockhand reaches it
#   <prefix>-registry      a registry of its own with a login, which the hosts push to and pull from
#   <prefix>-vault         a secret store that speaks Vault's API, empty and unsealed, for Dockhand to load a stack's secrets from
#   <prefix>-git           a git server with no repositories yet, for Dockhand to deploy a stack from, open and behind a login
#   <prefix>-backups       a restic server that holds nothing yet, for Dockhand to keep backups in
#
# Alpha also listens on the network the containers share, without TLS, so a
# suite can register it a third way, by address.
#
# Each host shares one folder with whoever deploys its stacks, at the same
# path on both, which is how a stack's files come to be on the host's own
# disk as they are where Dockhand runs beside its Docker: alpha with Dockhand
# itself, and beta with its agent. A backup of a stack reads them from there.
#
# This script only does what dockhand-mcp cannot: run the containers. Setting
# the server up - its first user, its login, an API token, the environments -
# is the suites' job, through the client and the tools, so that they are
# exercised rather than bypassed. The same goes for what runs on the hosts:
# the suites start it, through Dockhand where Dockhand can and inside the
# host's own container where the point is that Dockhand did not.
#
# Nothing but Dockhand's own port is published, and only on this machine.

set -euo pipefail

IMAGE="${DOCKHAND_TEST_IMAGE:-fnsys/dockhand:latest}"
DOCKER_IMAGE="${DOCKHAND_TEST_DOCKER_IMAGE:-docker:dind}"
AGENT_IMAGE="${DOCKHAND_TEST_AGENT_IMAGE:-ghcr.io/finsys/hawser:latest}"
REGISTRY_IMAGE="${DOCKHAND_TEST_REGISTRY_IMAGE:-registry:2}"
# the secret store: OpenBao, which speaks Vault's API and comes from a registry of its own rather than Docker Hub
VAULT_IMAGE="${DOCKHAND_TEST_VAULT_IMAGE:-ghcr.io/openbao/openbao:2.3.1}"
# where backups are kept: restic's own small server, from the mirror
BACKUP_IMAGE="${DOCKHAND_TEST_BACKUP_IMAGE:-mirror.gcr.io/restic/rest-server:0.14.0}"
# what the suites run on the hosts: small, and from the mirror, since each host pulls it for itself
FIXTURE_IMAGE="${DOCKHAND_TEST_FIXTURE_IMAGE:-mirror.gcr.io/library/busybox:1.37}"
# what the suites have scanned for vulnerabilities, and what scans it: an image old enough that a scanner finds plenty in it, and the scanner this Dockhand runs unless told, both from the mirror, since a host pulls each for itself
SCANNED_IMAGE="${DOCKHAND_TEST_SCANNED_IMAGE:-mirror.gcr.io/library/alpine:3.10}"
SCANNER_IMAGE="${DOCKHAND_TEST_SCANNER_IMAGE:-mirror.gcr.io/anchore/grype:v0.119.0}"
# every container, volume and network is named from the prefix, so two suites can each have a set
PREFIX="${DOCKHAND_TEST_CONTAINER:-dockhand-mcp-test}"
NETWORK="${PREFIX}-net"
PORT="${DOCKHAND_TEST_PORT:-14300}"
# the administrator the suites make, and the token the agent is started with
USERNAME="dockhand-mcp-test"
PASSWORD="dockhand-mcp-test-password"
AGENT_TOKEN="dockhand-mcp-test-agent-token"
# the registry's one login, the administrator's name and password above as the registry keeps them: a bcrypt line of a password that is made up and written three lines up
# shellcheck disable=SC2016 # the dollars are the hash's own, and nothing to expand
REGISTRY_LOGIN='dockhand-mcp-test:$2y$05$zvBX8sqsAUIfthCP63..muYT/wRRi.sbOEE2zPQPMQi7m5yNDJn1u'
# where Dockhand finds the git server, which speaks plain HTTP on the network they share, and the login its private half asks for: made up, and good for nothing but this server
GIT="http://git:8080"
GIT_USER="dockhand-mcp-test"
GIT_PASSWORD="dockhand-mcp-test-git-password"
# where the hosts and Dockhand find the registry, which speaks plain HTTP on the network they share
REGISTRY="registry:5000"
# where Dockhand finds the secret store, which speaks plain HTTP on the network they share, and the token it is started with: made up, and good for nothing but this store
VAULT="http://vault:8200"
VAULT_TOKEN="dockhand-mcp-test-vault-token"
# where Dockhand finds alpha's socket: the volume alpha keeps /var/run on
ALPHA_SOCKET="/run/alpha/docker.sock"

HOSTS="alpha beta"
URL="http://127.0.0.1:${PORT}"

# stacks_of is the folder a host shares with whoever deploys its stacks, at
# the same path on both: Dockhand's own folder for alpha, and the agent's for
# beta
stacks_of() {
  case "$1" in
    alpha) echo /app/data/stacks ;;
    *) echo /data/stacks ;;
  esac
}

need() {
  for tool in "$@"; do
    command -v "$tool" >/dev/null || { echo "testenv: $tool is required" >&2; exit 1; }
  done
}

is_running() {
  [ "$(docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" = "true" ]
}

# pull_image fetches an image when this machine does not have it: from
# Google's mirror of Docker Hub, which holds the same image under the same
# name and does not turn away a CI runner for what its shared address has
# pulled, and from Docker Hub itself when the mirror gives nothing - it can be
# a while behind a newly published image. An image already here is used as it
# is: docker pull it to test a newer one.
pull_image() {
  local image="$1" mirrored
  docker image inspect "$image" >/dev/null 2>&1 && return 0
  case "$image" in
    */*/*)
      # names a registry of its own: there is no mirror of it to try
      docker pull -q "$image" >/dev/null || {
        echo "testenv: could not pull ${image}" >&2
        exit 1
      }
      return 0
      ;;
    */*) mirrored="mirror.gcr.io/${image}" ;;
    # an official image, which Docker Hub and its mirror keep under library/
    *) mirrored="mirror.gcr.io/library/${image}" ;;
  esac
  if docker pull -q "$mirrored" >/dev/null 2>&1; then
    docker tag "$mirrored" "$image"
    return 0
  fi
  echo "testenv: mirror.gcr.io would not give ${image}: taking it from Docker Hub" >&2
  docker pull -q "$image" >/dev/null || {
    echo "testenv: could not pull ${image} from mirror.gcr.io or from Docker Hub" >&2
    exit 1
  }
}

# start_host runs a Docker host: a Docker of its own in a container, which
# has to be privileged to run containers itself. It keeps /var/run on a
# volume, which is how whatever is to reach its socket is handed it, and
# listens on the shared network as well, without TLS: nothing outside that
# network can reach it. It is told the registry speaks plain HTTP, which a
# Docker otherwise refuses to push to.
start_host() {
  local name="$1"
  docker volume create "${PREFIX}-run-${name}" >/dev/null
  docker volume create "${PREFIX}-stacks-${name}" >/dev/null
  docker run -d --name "${PREFIX}-docker-${name}" --hostname "docker-${name}" --network "$NETWORK" --network-alias "docker-${name}" \
    --privileged \
    -e DOCKER_TLS_CERTDIR= \
    -v "${PREFIX}-run-${name}:/var/run" \
    -v "${PREFIX}-stacks-${name}:$(stacks_of "$name")" \
    "$DOCKER_IMAGE" "--insecure-registry=${REGISTRY}" >/dev/null
}

# start_registry runs a registry that asks for a login and lets a tag be
# deleted, which a registry as it comes does not. Its login is written into
# it as it starts.
start_registry() {
  docker run -d --name "${PREFIX}-registry" --hostname registry --network "$NETWORK" --network-alias registry \
    -e "REGISTRY_LOGIN=${REGISTRY_LOGIN}" \
    -e REGISTRY_AUTH=htpasswd \
    -e REGISTRY_AUTH_HTPASSWD_REALM=dockhand-mcp-test \
    -e REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd \
    -e REGISTRY_STORAGE_DELETE_ENABLED=true \
    --entrypoint sh \
    "$REGISTRY_IMAGE" -c 'mkdir -p /auth && printf "%s\n" "$REGISTRY_LOGIN" > /auth/htpasswd && exec registry serve /etc/docker/registry/config.yml' >/dev/null
}

# start_vault runs the secret store the way its own documentation says to
# for a test: in memory, unsealed, with the token given it and one place for
# secrets, at secret/. It holds nothing until a suite puts something there.
start_vault() {
  docker run -d --name "${PREFIX}-vault" --hostname vault --network "$NETWORK" --network-alias vault \
    --cap-add IPC_LOCK \
    -e "BAO_DEV_ROOT_TOKEN_ID=${VAULT_TOKEN}" \
    -e BAO_DEV_LISTEN_ADDRESS=0.0.0.0:8200 \
    "$VAULT_IMAGE" >/dev/null
}

# wait_vault waits for the secret store to answer, which it does within a
# second or two of starting
wait_vault() {
  local container="${PREFIX}-vault"
  for _ in $(seq 1 60); do
    if docker exec -e BAO_ADDR=http://127.0.0.1:8200 "$container" bao status >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "testenv: the secret store in ${container} did not answer within 60s" >&2
  docker logs --tail 40 "$container" >&2 || true
  exit 1
}

# start_backups runs the server backups are kept on, asking for no login:
# nothing outside the network can reach it, and each repository on it has a
# password of its own. It holds nothing until a suite has Dockhand make a
# repository there.
start_backups() {
  docker run -d --name "${PREFIX}-backups" --hostname backups --network "$NETWORK" --network-alias backups \
    -e DISABLE_AUTHENTICATION=1 \
    "$BACKUP_IMAGE" >/dev/null
}

# backups_address is where the backup server is, by its address on the
# network and not its name: a backup is run by a container on a Docker host,
# which finds no name of this network, and the address is one Dockhand and
# the hosts both reach.
backups_address() {
  docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "${PREFIX}-backups"
}

# start_git runs the git server: .scripts/gitserver.js, run from the Dockhand
# image itself, which already carries git and node, so nothing more is
# pulled. It serves whatever repositories a suite makes under /srv/git, at
# /open/<name>.git to anyone and at /private/<name>.git to its one login.
start_git() {
  local container="${PREFIX}-git"
  docker create --name "$container" --hostname git --network "$NETWORK" --network-alias git \
    -e "GIT_USER=${GIT_USER}" \
    -e "GIT_PASSWORD=${GIT_PASSWORD}" \
    --entrypoint node \
    "$IMAGE" /gitserver.js >/dev/null
  docker cp "$(dirname "$0")/gitserver.js" "${container}:/gitserver.js" >/dev/null
  docker start "$container" >/dev/null
}

# wait_git waits for the git server to answer, which it does within a second
# of starting, and then tells git who commits there and which branch a new
# repository starts on
wait_git() {
  local container="${PREFIX}-git"
  for _ in $(seq 1 60); do
    if docker exec "$container" node -e "require('node:http').get('http://127.0.0.1:8080/open/none.git/info/refs', r => process.exit(r.statusCode === 404 ? 0 : 1)).on('error', () => process.exit(1))" >/dev/null 2>&1; then
      docker exec "$container" sh -c 'git config --global user.email suite@example.invalid && git config --global user.name suite && git config --global init.defaultBranch main && mkdir -p /srv/git /srv/work' >/dev/null
      return 0
    fi
    sleep 1
  done
  echo "testenv: the git server in ${container} did not answer within 60s" >&2
  docker logs --tail 40 "$container" >&2 || true
  exit 1
}

# wait_host waits for a host's Docker to answer, which takes it a quarter of a
# minute from a cold start
wait_host() {
  local name="$1" container="${PREFIX}-docker-$1"
  for _ in $(seq 1 90); do
    if docker exec "$container" docker info >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "testenv: the Docker in ${container} did not answer within 90s" >&2
  docker logs --tail 40 "$container" >&2 || true
  exit 1
}

# start_agent runs a Hawser agent in front of a host, in the mode that
# listens for Dockhand rather than calling it, with the host's socket from
# the volume the host keeps it on
start_agent() {
  local name="$1"
  docker run -d --name "${PREFIX}-agent-${name}" --hostname "agent-${name}" --network "$NETWORK" --network-alias "agent-${name}" \
    -e "TOKEN=${AGENT_TOKEN}" \
    -e "AGENT_NAME=agent-${name}" \
    -v "${PREFIX}-run-${name}:/var/run" \
    -v "${PREFIX}-stacks-${name}:$(stacks_of "$name")" \
    "$AGENT_IMAGE" >/dev/null
}

# start_server runs Dockhand itself, with alpha's socket where an environment
# of the socket kind can name it, the folder it shares with alpha for stacks'
# files, and its backups switched on: they are off as Dockhand comes, and
# every backup call answers 404 until they are on
start_server() {
  docker run -d --name "$PREFIX" --hostname dockhand --network "$NETWORK" --network-alias dockhand \
    -p "127.0.0.1:${PORT}:3000" \
    -e FEAT_BACKUPS_ENABLED=true \
    -v "${PREFIX}-run-alpha:/run/alpha" \
    -v "${PREFIX}-stacks-alpha:$(stacks_of alpha)" \
    "$IMAGE" >/dev/null
}

# wait_server waits for Dockhand's API to answer, which it does a few seconds
# after the container starts
wait_server() {
  for _ in $(seq 1 90); do
    if curl -sf "${URL}/api/health" 2>/dev/null | jq -e '.status == "ok"' >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "testenv: ${PREFIX} did not answer on ${URL} within 90s" >&2
  docker logs --tail 40 "$PREFIX" >&2 || true
  exit 1
}

print_env() {
  # everything a person reads goes to stderr: stdout is what eval takes
  echo "testenv: ${PREFIX} up (${IMAGE}), with hosts ${HOSTS} (${DOCKER_IMAGE}), an agent before beta (${AGENT_IMAGE}), a registry (${REGISTRY_IMAGE}), a secret store (${VAULT_IMAGE}), a git server and a backup server (${BACKUP_IMAGE})" >&2
  echo "export DOCKHAND_SERVER='${URL}'"
  echo "export DOCKHAND_TEST_CONTAINER='${PREFIX}'"
  echo "export DOCKHAND_TEST_NETWORK='${NETWORK}'"
  echo "export DOCKHAND_TEST_USER='${USERNAME}'"
  echo "export DOCKHAND_TEST_PASSWORD='${PASSWORD}'"
  # the containers the hosts run in, which a suite starts things inside
  echo "export DOCKHAND_TEST_HOSTS='alpha=${PREFIX}-docker-alpha,beta=${PREFIX}-docker-beta'"
  # the three ways Dockhand reaches a host: alpha's socket, alpha's address, and the agent before beta
  echo "export DOCKHAND_TEST_SOCKET='${ALPHA_SOCKET}'"
  echo "export DOCKHAND_TEST_DIRECT='docker-alpha:2375'"
  echo "export DOCKHAND_TEST_AGENT='agent-beta:2376'"
  echo "export DOCKHAND_TEST_AGENT_TOKEN='${AGENT_TOKEN}'"
  echo "export DOCKHAND_TEST_AGENT_IMAGE='${AGENT_IMAGE}'"
  # the registry, as the hosts and Dockhand name it: its login is the administrator's name and password
  echo "export DOCKHAND_TEST_REGISTRY='${REGISTRY}'"
  # the secret store, as Dockhand names it, and the token it takes
  echo "export DOCKHAND_TEST_VAULT='${VAULT}'"
  echo "export DOCKHAND_TEST_VAULT_TOKEN='${VAULT_TOKEN}'"
  # the git server, as Dockhand names it, and the login its private half asks for
  echo "export DOCKHAND_TEST_GIT='${GIT}'"
  echo "export DOCKHAND_TEST_GIT_USER='${GIT_USER}'"
  echo "export DOCKHAND_TEST_GIT_PASSWORD='${GIT_PASSWORD}'"
  # the backup server, as restic names a repository on it: a suite adds a name of its own to the end
  echo "export DOCKHAND_TEST_BACKUP='rest:http://$(backups_address):8000'"
  echo "export DOCKHAND_TEST_FIXTURE_IMAGE='${FIXTURE_IMAGE}'"
  echo "export DOCKHAND_TEST_SCANNED_IMAGE='${SCANNED_IMAGE}'"
  echo "export DOCKHAND_TEST_SCANNER_IMAGE='${SCANNER_IMAGE}'"
}

up() {
  need docker curl jq
  down >/dev/null 2>&1 || true
  pull_image "$IMAGE"
  pull_image "$DOCKER_IMAGE"
  pull_image "$AGENT_IMAGE"
  pull_image "$REGISTRY_IMAGE"
  pull_image "$VAULT_IMAGE"
  pull_image "$BACKUP_IMAGE"

  docker network create "$NETWORK" >/dev/null
  start_registry
  start_vault
  start_git
  start_backups
  local name
  for name in $HOSTS; do
    start_host "$name"
  done
  for name in $HOSTS; do
    wait_host "$name"
  done
  wait_vault
  wait_git
  start_agent beta
  start_server
  wait_server
  print_env
}

# down removes what up started, each by its own name, and nothing else: the
# server, the registry, the secret store, the git server, the backup server,
# the agents, the one up started and any a suite started to call in, the
# hosts with everything that ran on them, and the volumes and network they
# shared
down() {
  need docker
  local name
  docker rm -f -v "$PREFIX" >/dev/null 2>&1 || true
  docker rm -f -v "${PREFIX}-registry" >/dev/null 2>&1 || true
  docker rm -f -v "${PREFIX}-vault" >/dev/null 2>&1 || true
  docker rm -f -v "${PREFIX}-git" >/dev/null 2>&1 || true
  docker rm -f -v "${PREFIX}-backups" >/dev/null 2>&1 || true
  for name in $HOSTS; do
    docker rm -f -v "${PREFIX}-agent-${name}" >/dev/null 2>&1 || true
    # an agent that calls in, which a suite starts for itself and takes away again unless it was cut short
    docker rm -f -v "${PREFIX}-caller-${name}" >/dev/null 2>&1 || true
    docker rm -f -v "${PREFIX}-docker-${name}" >/dev/null 2>&1 || true
  done
  for name in $HOSTS; do
    docker volume rm "${PREFIX}-run-${name}" >/dev/null 2>&1 || true
    docker volume rm "${PREFIX}-stacks-${name}" >/dev/null 2>&1 || true
  done
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
}

# spec prints the API document the image's own server serves about itself,
# from a server started for the purpose alone and removed again: no hosts, and
# a name and port of its own, so it can be asked while a suite runs. With
# "latest" for an image the newest is pulled first, since an image already
# here would otherwise be read as the newest there is.
spec() {
  need docker curl jq
  local name="${PREFIX}-spec" port="$((PORT + 200))"
  case "$IMAGE" in
    *:latest) docker image rm "$IMAGE" >/dev/null 2>&1 || true ;;
  esac
  pull_image "$IMAGE"
  docker rm -f -v "$name" >/dev/null 2>&1 || true
  docker run -d --name "$name" -p "127.0.0.1:${port}:3000" "$IMAGE" >/dev/null
  trap 'docker rm -f -v "'"$name"'" >/dev/null 2>&1 || true' EXIT
  for _ in $(seq 1 90); do
    if curl -sf "http://127.0.0.1:${port}/api/health" >/dev/null 2>&1; then
      # written out the way jq lays it, so a document that says the same reads the same
      curl -sf "http://127.0.0.1:${port}/openapi.json" | jq .
      return 0
    fi
    sleep 1
  done
  echo "testenv: ${name} did not answer within 90s" >&2
  docker logs --tail 40 "$name" >&2 || true
  exit 1
}

logs() {
  need docker
  local container
  for container in "$PREFIX" "${PREFIX}-agent-beta" "${PREFIX}-docker-alpha" "${PREFIX}-docker-beta" "${PREFIX}-registry" "${PREFIX}-vault" "${PREFIX}-git" "${PREFIX}-backups"; do
    is_running "$container" || continue
    echo "==> ${container}" >&2
    docker logs --tail 200 "$container" >&2 || true
  done
}

case "${1:-up}" in
  up) up ;;
  down) down ;;
  logs) logs ;;
  spec) spec ;;
  *)
    echo "usage: $0 up | down | logs | spec" >&2
    exit 1
    ;;
esac
