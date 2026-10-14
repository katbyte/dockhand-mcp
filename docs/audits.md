# Audits

Thirteen tools that look across every Docker host for what needs attention. They load by default and only read.

Start with `audit_all`. It gives a count for each audit. Then call the audit whose count is not zero.

## How every audit works

- **Every host unless one is named.** `env` narrows an audit to one host. Most also take `stack`, to look at one compose stack.
- **A host that could not be asked is named.** It is listed under `failed` with why. Nothing on it was checked.
- **Each finding is one line.** It names the host, the thing, the check that failed as one word, and the same in a sentence.
- **The list is cut, the counts are not.** `limit` is 100 unless given, and 1,000 at most. `total` and `by_check` count every finding.
- **Nothing to look at is not a clean bill.** `checked` says how many things were looked at. A total of 0 beside a `checked` of 0 means nothing was there.

## audit_all

Runs every audit over every host, or over one, and gives only the counts.

- Each row names the audit, how many findings it has, and how many of each check.
- The audits are listed with what is broken now first, then what will break, then what is only untidy.
- Every container is inspected once for all of them. That takes a few seconds a host, and longer with many containers.

## audit_health

Finds the containers that are broken right now. Every container is inspected, running or not.

| Check | What it means |
|---|---|
| `unhealthy` | its own health check is failing; the finding gives what the latest check printed |
| `restarting` | Docker keeps starting it again |
| `restart_loop` | it is running, and was started again only seconds ago |
| `exited_with_error` | it stopped with a code other than 0 |
| `out_of_memory` | the host killed it for memory |
| `start_failed` | Docker could not start it; the finding says why |
| `dead` | Docker marks it dead |

An exit code of 137 is how a container ends when it is stopped and does not stop by itself. The finding says so.

**Then:** `container_logs` says what one wrote before it fell over. `container_get` gives one in full. `container_restart` starts it again once the cause is dealt with.

## audit_hosts

Finds the Docker hosts Dockhand cannot reach: `offline`.

Nothing on such a host is seen by any other audit. A clean answer from the others says nothing about it.

**Then:** `env_test` tries a host's connection and says why it fails.

## audit_stacks

Finds the compose stacks that are not wholly up.

| Check | What it means |
|---|---|
| `partly_running` | some of its containers run and some do not; each is named with its state |
| `not_running` | none of its containers runs |

A stack taken down on purpose is found too. Nothing tells the two apart.

**Then:** `stack_list` gives each stack's containers and `stack_logs` what they wrote. `stack_get` gives one in full, with what its last deploy printed. `stack_start` or `stack_deploy` brings it up.

## audit_healthcheck

Finds the running containers nothing checks. Only running containers are looked at.

| Check | What it means |
|---|---|
| `no_health_check` | neither its image nor whoever made it gave it one |
| `health_check_off` | it was given one and made with it switched off |

Docker calls such a container running for as long as its process lives, whether or not it still answers. So `audit_health` can never find it unhealthy.

**Then:** no tool sets a health check on a container. For a stack, add one to its compose file with `stack_edit` and bring it up again with `stack_deploy`.

## audit_restart_policy

Finds the running containers that would stay down: `no_restart_policy`. Docker does not start such a container again when it exits or when the host reboots.

Only running containers are looked at. One that ran once and finished is meant to stay stopped.

**Then:** `container_limits` sets a restart policy without making the container again.

## audit_updates

Finds the containers a newer image is to be had for, as Dockhand last noted it.

| Check | What it means |
|---|---|
| `newer_image` | the registry has another image under the very name and tag it runs |
| `newer_version` | a newer version tag exists; the finding says how big a step it is |

It reads what the last check found and asks no registry itself.

**Then:** `container_updates` asks the registries afresh. `container_update` updates a container.

## audit_vulnerabilities

Finds the images with grave known vulnerabilities, as the scans last found them.

| Check | What it means |
|---|---|
| `critical_vulnerabilities` | one finding an image: how many, how many a newer version puts right, and the containers made from it |
| `high_vulnerabilities` | the same, for high ones |
| `images_not_scanned` | one finding a host: how many of its images no scan has looked at |

Images that were never scanned are in no count. The audit reads what the scans found and scans nothing.

**Then:** `image_scan_all` scans a host. `vulnerability_list` lists each vulnerability.

## audit_security

Finds the containers given more of their host than a container needs.

| Check | What it means |
|---|---|
| `privileged` | it has every capability of the host |
| `host_network` | it is on the host's own network, with no ports of its own to close |
| `host_pid` | it sees and can signal every process of the host |
| `docker_socket` | the Docker socket is mounted, which is control of the host |
| `host_root` | the whole of the host's disk is mounted |
| `added_capabilities` | it was given capabilities that reach outside it; each is named |

Dockhand's own containers need the Docker socket and are passed over. Some software needs these. Each finding is something to have decided, not necessarily a fault.

**Then:** no tool takes these away from a container. For a stack, change its compose file with `stack_edit` and bring it up again with `stack_deploy`.

## audit_image_tags

Finds the containers whose image does not say which version they run.

| Check | What it means |
|---|---|
| `latest_tag` | what runs changes whenever the image is pulled again, and nothing records what it was |
| `no_tag` | the same: no tag is `latest` |
| `image_without_name` | it was made from an image by its id, which no update check can follow |

An image pinned by its digest is exact and is passed over.

**Then:** for a stack, name a version in its compose file with `stack_edit` and bring it up again with `stack_deploy`.

## audit_limits

Finds the running containers with no memory limit: `no_memory_limit`. Such a container may use all of the host's memory, so one that leaks takes the others down with it.

Only running containers are looked at.

**Then:** `container_limits` sets one without making the container again.

## audit_stopped

Finds the containers left lying stopped.

| Check | What it means |
|---|---|
| `stopped_long` | not running for more than `days`, 7 unless given; the finding says when it stopped and what it exited with |
| `never_started` | made that long ago and never run |

They hold their image and their volumes in use, so neither is pruned.

**Then:** `container_delete` removes one. `container_prune` removes every stopped one of a host.

## audit_unused

Finds what a host holds that nothing uses.

| Check | What it means |
|---|---|
| `unused_image` | no container was made from it; the finding gives its size |
| `unused_volume` | no container mounts it |
| `unused_network` | no container is on it |

The three networks every host has are passed over. The note adds up the images' sizes.

A volume nothing mounts may still hold data someone wants. Look at each before it is removed.

**Then:** `image_delete`, `volume_delete` and `network_delete` remove one. `image_prune`, `volume_prune` and `network_prune` remove every unused one.

## From a finding to a fix

The audits say where to look:

```text
audit_all                             every audit over every host, counted: where to look
audit_health                          what is broken now, each with why: the health check's words, the exit code
audit_healthcheck                     what could never be found unhealthy, because nothing checks it
audit_security env=<host>             what was given more of its host than a container needs
```

Then one container is looked into, with the `core` tools:

```text
container_list problems=true          every container that is unhealthy, restarting or exited with an error, on every host
container_get container=<name>        why: what its health check printed, its exit code, whether the host killed it for memory
container_logs container=<name>       what it wrote before it stopped
stack_logs stack=<name>               what its neighbours wrote around the same time
container_restart container=<name>    once the cause is dealt with
```
