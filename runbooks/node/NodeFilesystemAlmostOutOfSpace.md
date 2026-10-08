# NodeFilesystemAlmostOutOfSpace

**Severity:** warning below 5% free, critical below 3%. **Owner:** platform on-call.

A filesystem on a node has had less than 5% free space for 30 minutes. Writes will start failing when it fills. Set `MOUNTPOINT` from the alert's `mountpoint` label.

## Diagnose

On the node, confirm the usage and find what is taking the space:

```sh {"name":"usage","drill":"check","target":"node"}
df -h "$MOUNTPOINT"
du -xah "$MOUNTPOINT" 2>/dev/null | sort -h | tail -n 10
```

## Fix

The usual cause on application data volumes is rotated logs that nothing ships or deletes. Remove rotated files (`*.log.1`, `*.log.2.gz` and so on); the live `*.log` files stay:

```sh {"name":"delete-rotated-logs","drill":"fix","target":"node"}
find "$MOUNTPOINT" -xdev -type f -name '*.log.[0-9]*' -print -delete
df -h "$MOUNTPOINT"
```

The alert has no hold-down on resolution, so it clears at the next rule evaluation, within about a minute.

## Escalate

If rotated logs are not the cause, do not delete data you cannot identify. Grow the volume or move the workload, and page the owning team.
