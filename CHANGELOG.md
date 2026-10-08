# Changelog

## v0.1.0

First release.

### Drills

- `drillbook run` injects a fault, waits for the alert, reads its Alertmanager receivers, runs the runbook's tagged blocks, waits for the alert to clear, reverts, and runs a cached control without the runbook.
- Nine verdicts, from `pass` to `revert-failed`, with every finding listed.
- Drills silence their own alert for their target only, and keep the silence until the alert clears so nobody is paged.
- Preflight waits for alerts left over from an earlier drill before giving up.
- Drills can pick which fix blocks to run with `fixes:`.

### Faults

- Ansible collection `drillbook.faults` with `stop_service`, `fill_filesystem` and `stop_container`. Every fault arms a `systemd-run` revert timer on the host first.
- Kubernetes faults scale or patch a Deployment after saving a snapshot to restore.

### Commands

- `run`, `plan`, `lint`, `affected`, `abort`, `list`, `report` (markdown or Prometheus textfile) and `version`.
- `run --github-issues owner/name` keeps one issue per failing drill.

### Packaging

- Linux and macOS binaries, a container image on GHCR, the Ansible collection tarball, and a Helm chart that runs cluster-only drills from a CronJob.

### Drill environment

- Terraform and Ansible build a two-node kubeadm cluster with kube-prometheus-stack and a demo service on the local Docker daemon.
- Five drills with runbooks: `shop-api-scaled-to-zero`, `shop-api-crashloop`, `shop-api-bad-image`, `kubelet-stopped` and `app-data-disk-full`.
