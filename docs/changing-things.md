# Before changing anything

Out of the box every tool only reads. This is what to know before switching changes on.

- **Changes are off until switched on.** Tools that change something load only with `--enable-write`. Tools that remove something load only with `--enable-delete` as well.
- **A change can be tried first.** A change takes `dry_run` and says what it would do. `login_set` asks for `confirm` instead. A removal says what would go, and removes nothing until called with `confirm`.
- **Changing a container makes it again.** `container_edit` and `container_update` remove the container and make it again under the same name with the change. Anything it wrote outside its volumes is lost. `container_limits` is the exception: memory, processors and the restart policy are set as it runs.
- **A prune lists what it would take.** `volume_prune` removes named volumes too, which `docker volume prune` leaves. `network_prune` would take the networks of a stack that is only stopped, which then will not start. So it refuses unless `stranding` is given.
- **Removing a stack removes its containers.** `stack_down` is the gentler one: the containers go, the compose file stays, and `stack_deploy` brings it back.
- **Looking inside a volume needs `--enable-write`.** `volume_files` and `volume_read` change nothing. But Dockhand has the host start a small helper container to look, which a session told only to read should not be doing.
- **Automatic updates restart things with nobody watching.** `container_autoupdate_set` and a host's `auto_update` schedule pull newer images and make containers again on their own.
- **Backups are off unless Dockhand was started with them.** They are a beta its container has only with `FEAT_BACKUPS_ENABLED=true`. Until then every backup tool says so.
- **A restore writes beside what is there unless told otherwise.** `backup_restore` with `in_place` replaces a volume whole. It needs `--enable-delete` and `confirm`.
- **Slow work does not hold a session up.** A pull, a deploy, a prune or a backup still going after 50 seconds answers with a job id. `job_get` says how it went.

Dockhand saves a good deal without checking it, and some of what it does is a surprise. The tools check first and say so in their own descriptions. What Dockhand does that it does not document is written up in [api-defs/README.md](../api-defs/README.md).
