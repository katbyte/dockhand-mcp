# Tools

There are 225 tools. They are named for what they act on: `container_list`, `stack_deploy`, `image_pull`.

A tool about a Docker host takes `env`, the host's name or number as `env_list` gives it. Left out, every host is asked.

This prints every tool with what it does, and needs no server:

```sh
dockhand-mcp tools --toolsets all --enable-write --enable-delete
```

Which of them load is chosen by group: see [toolsets.md](toolsets.md). What to know before letting them change something is in [changing-things.md](changing-things.md).

## Audits

Each audit is written up in [audits.md](audits.md).

`audit_all` (every audit over every host, counts only: start here), `audit_health` (containers broken right now), `audit_hosts` (hosts out of reach), `audit_stacks` (stacks not wholly up), `audit_healthcheck` (running containers nothing checks), `audit_restart_policy` (would stay down after a reboot), `audit_updates` (a newer image is out), `audit_vulnerabilities` (images with critical or high vulnerabilities, or never scanned), `audit_security` (containers given more of their host than they need), `audit_image_tags` (on `latest` or no tag), `audit_limits` (no memory limit), `audit_stopped` (left stopped for days), `audit_unused` (images, volumes and networks nothing uses)

## Server

`server_info` (connection, version, whether the login is on), `server_update_check`, `server_about`, and for Dockhand's own disk, where the compose files of its stacks are, `server_files`, `server_read`, `server_mkdir`

## Hosts (environments)

`env_list`, `env_get`, `env_test`, `env_create`, `env_edit`, `env_schedules` (what Dockhand does for a host by itself, and when), `env_delete` (forget a host, leaving what runs on it), `env_disk` (what fills its disk)

## Agents

`agent_list`, `agent_token_create`, `agent_token_delete` (the Hawser agents that call in to Dockhand, and their tokens)

## Containers

`container_list`, `container_get`, `container_logs`, `container_start`, `container_stop`, `container_restart`, `container_stats`, `container_sizes`, `container_top` (its processes), `container_compose` (one written out as a compose file), `container_events` (what containers did and when), `container_create`, `container_edit`, `container_limits`, `container_pause`, `container_unpause`, `container_rename`, `container_exec`, `container_delete`, `container_prune`, `container_events_clear`

## Files in a container

`container_files`, `container_read`, `container_file_download`, `container_file_write`, `container_file_upload`, `container_file_mkdir`, `container_file_move`, `container_file_perms`, `container_file_delete`

## Updates

`container_updates` (which have a newer image out), `container_update`, `container_updates_clear`, `container_autoupdate`, `container_autoupdate_set` (which containers Dockhand updates by itself)

## Stacks

`stack_list`, `stack_get`, `stack_logs`, `stack_start`, `stack_stop`, `stack_restart`, `stack_validate`, `stack_deploys` (each time one was brought up), `stack_scan` (look for compose files to adopt), `stack_create`, `stack_edit`, `stack_deploy`, `stack_down` (remove its containers, keep its file), `stack_adopt` (take on a stack someone else brought up), `stack_move`, `stack_delete`, `stack_deploys_delete`

## Images

`image_list`, `image_get`, `image_pull`, `image_tag`, `image_delete`, `image_prune`, `image_export`, `image_load` (to and from a file on this machine)

## Volumes

`volume_list`, `volume_get`, `volume_files`, `volume_read`, `volume_create`, `volume_clone`, `volume_delete`, `volume_prune`, `volume_export`

## Networks

`network_list`, `network_get`, `network_create`, `network_connect`, `network_disconnect`, `network_delete`, `network_prune`

## Registries

`registry_list`, `registry_test`, `registry_images`, `registry_tags`, `registry_create`, `registry_edit`, `registry_delete`, `registry_image_delete`, `image_push`

## Schedules

`schedule_list` (everything Dockhand does by itself), `schedule_history` (how each run went), `schedule_run`, `schedule_set` (switch one off or on), `schedule_delete`, `schedule_history_delete`

## Notifications

`notification_list`, `notification_test`, `notification_create`, `notification_edit`, `notification_link` (tie a channel to a host), `notification_link_delete`, `notification_delete`

## Vulnerabilities

`vulnerability_list`, `vulnerability_export`, `image_scan`, `image_scan_all`, `image_scan_get`, `scanner_get`, `scanner_set`, `scanner_delete`, `scanner_cache_clear`

## Users and login

`user_list`, `user_create`, `user_edit`, `user_delete`, `login_get`, `login_set`, `token_list`, `sso_list`, `sso_create`, `sso_edit`, `sso_delete`

## Settings

`settings_get`, `settings_set`, `preferences_get`, `preferences_set` (what the web interface remembers), `favorite_list`, `favorite_set` (the containers kept at hand in a log view)

## Secret stores

`secret_provider_list`, `secret_provider_test`, `secret_provider_keys` (names only, never values), `secret_provider_create`, `secret_provider_edit`, `secret_provider_delete`

## Backups

`backup_destination_list`, `backup_destination_test`, `backup_destination_check` (how much one holds, and whether it is sound), `backup_destination_create`, `backup_destination_edit`, `backup_destination_task` (unlock, prune, repair), `backup_destination_rotate_key`, `backup_destination_delete`, `backup_list`, `backup_create`, `backup_edit`, `backup_run`, `backup_stop`, `backup_delete`, `backup_snapshot_list`, `backup_snapshot_get`, `backup_snapshot_files`, `backup_snapshot_read`, `backup_snapshot_diff`, `backup_snapshot_download` (a file or a folder out of one, to this machine), `backup_snapshot_delete`, `backup_restore`

## Git

`git_credential_list`, `git_credential_create`, `git_credential_edit`, `git_credential_delete`, `git_repository_list`, `git_repository_test`, `git_repository_variables`, `git_repository_create`, `git_repository_edit`, `git_repository_delete`, `git_stack_list`, `git_stack_get`, `git_stack_create`, `git_stack_edit`, `git_stack_sync`, `git_stack_deploy`, `git_stack_delete`

## Templates

`template_list`, `template_get`, `template_source_list`, `template_source_create`, `template_source_edit`, `template_source_delete`

## Config sets

`config_set_list`, `config_set_create`, `config_set_edit`, `config_set_delete` (bundles of container settings to hand to `container_create`)

## Tags, labels and icons

`tag_list`, `tag_create`, `tag_edit`, `tag_assign`, `tag_delete`, `label_list`, `label_edit`, `label_assign`, `label_delete` (labels are for hosts), `icon_list`, `icon_search`, `icon_set`, `icon_download`, `icon_delete`

## Jobs

`job_get`, `job_cancel` (slow work left running)
