# drillbook.faults

Faults that `drillbook` injects during a fire drill. Each role has two task files:

- `apply`: arms a revert timer on the target with `systemd-run --on-active`, then applies the fault. If the drill runner dies, the timer undoes the fault on its own.
- `revert`: cancels the timer and undoes the fault. Running it twice is harmless.

Every role takes `drillbook_drill_id` (names the timer unit `drillbook-revert-<id>`) and, for `apply`, `drillbook_revert_after` in seconds.

| Role | Variables | Fault |
|---|---|---|
| `stop_service` | `stop_service_name` | `systemctl stop` a unit. Refuses `ssh` and `sshd`, which the revert needs. |
| `fill_filesystem` | `fill_filesystem_path`, `fill_filesystem_file`, `fill_filesystem_leave_free_percent` (4), `fill_filesystem_max_fs_bytes` (2 GiB) | Writes one file with `fallocate` until the filesystem has the given percent free. Refuses filesystems larger than the limit. |
| `stop_container` | `stop_container_name` | `docker stop` a container. |

The timer runs in the system manager when the remote user is root and in the user manager otherwise.
