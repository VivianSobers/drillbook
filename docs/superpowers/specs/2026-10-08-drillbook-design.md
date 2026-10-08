# drillbook design

Date: 2026-10-08
Status: v0.1 built; see "Decisions made while building v0.1" at the end

## Summary

drillbook runs fire drills on Prometheus alerts and the runbooks they link to. A drill breaks something on purpose, checks that the right alert fires, runs the fix written in the runbook, and checks that the alert clears. When any step fails, drillbook opens an issue. Over time it keeps a record of which alerts have been shown to fire and which runbooks have been shown to work, and when each was last checked.

It is a command-line tool written in Go. Faults on machines and runbook steps that run on machines go through Ansible over SSH. A Terraform and Ansible module builds a throwaway kubeadm cluster for drills that are too destructive for a shared staging environment.

## Problem

Alerts and runbooks fail quietly. An alert rule can reference a metric that an exporter upgrade renamed, so it never fires. An alert can fire and route to the wrong team. A runbook can name a namespace, flag or command that no longer exists, and nobody notices until someone follows it during an incident.

Existing tools cover parts of this:

- `promtool test rules` checks rule logic against synthetic series.
- `pint` checks rules against a live Prometheus, including whether the series they use exist.
- Chaos tools such as LitmusChaos and Krkn inject faults and can query Prometheus during an experiment, so they can check that an alert fires.
- Runme lets people run the code blocks in a markdown runbook by hand.
- Harness sells scheduled checks of disaster-recovery runbooks as a commercial product.

No open-source tool runs the runbook as part of a drill and checks that it clears the alert. drillbook does that and builds on the tools above where they already do the job.

The pattern already exists by hand in Private-Cloud, where `scripts/dr-drill.sh` (written by Guru Bharadwaj) rehearses the disaster-recovery runbook every month, and the alerts `DrDrillTooOld` and `DrDrillMetricsMissing` fire when the rehearsal stops running. drillbook makes that pattern available for every alert.

## Users

- A platform or SRE team running Kubernetes on their own hardware with kube-prometheus-stack, which wants proof that its alerts and runbooks work.
- A team running services on plain Linux hosts with Prometheus and Alertmanager, such as Private-Cloud.

## Goals

- Run a drill end to end from one command: fault, alert fires, runbook steps, alert clears, cleanup.
- Use the runbook file itself as the source of the steps, so a drill fails when the runbook goes stale.
- Never page a human during a drill, and never leave a machine broken if the runner dies.
- Make results visible: exit codes and JSON for CI, Prometheus metrics, a Grafana dashboard, GitHub issues on failure.
- Ship a drill pack for kube-prometheus-stack's default alerts so the tool is useful on install.

## Non-goals for v0.1

- A Kubernetes operator or CRDs. Drills are files in a git repository.
- Running drills against production.
- Runbooks stored outside git (Confluence, wikis).
- Monitoring systems other than Prometheus and Alertmanager.
- Fault injection that a chaos tool already does well inside pods. drillbook covers the simple pod faults it needs itself and leaves the rest to those tools.

## How a drill works

1. Preflight. Load the drill file and the runbook. Check that Prometheus and Alertmanager are reachable, the target is on the allowlist, and the alert is not already firing for that target.
2. Silence. Create an Alertmanager silence that matches the alert name and the target's labels, for the length of the drill plus a margin. Prometheus still evaluates the rule, so the `ALERTS` series still shows the alert firing.
3. Arm the revert. For a host fault, schedule the undo on the host before applying the fault, with `systemd-run --on-active=<limit>`. If the runner dies, the host reverts on its own.
4. Inject. Apply the fault through its executor.
5. Expect the alert. Poll Prometheus for `ALERTS{alertname=..., alertstate="firing"}` with the target's labels until it fires or `fire_within` runs out. Then confirm the alert reached Alertmanager and record which receiver it would have notified.
6. Run the runbook. Run the blocks tagged `fix` in the runbook, in document order, on the targets they name. Blocks tagged `check` run first and must exit 0. They prove the diagnostic commands still work.
7. Expect resolution. Poll until the alert stops firing or `resolve_within` runs out. Record time to resolve.
8. Clean up. Remove whatever the fault left behind, cancel the revert timer, delete the silence. Cleanup runs on every path, including failures and interrupts.
9. Control run, only after a pass. Inject the same fault without running the runbook. If the alert clears anyway, the system healed itself and the verdict becomes `inconclusive`. The result is cached per fault and alert until the drill file changes.

### Verdicts

| Verdict | Meaning |
|---|---|
| `pass` | Alert fired, runbook ran, alert cleared within the limit, control did not clear |
| `alert-did-not-fire` | Fault was applied, alert never fired within `fire_within` |
| `misrouted` | Alert fired but would reach a receiver other than `expect.receiver` |
| `step-failed` | A `check` or `fix` block exited non-zero |
| `not-resolved` | Steps ran, alert still firing after `resolve_within` |
| `slower-than-runbook` | Resolved, but slower than the runbook's stated `expected_resolve` |
| `inconclusive` | Control run cleared without the runbook |
| `aborted` | Preflight failed, or the drill was cancelled; nothing to judge |

## Runbook format

Runbooks stay ordinary markdown that people read. drillbook only looks at fenced code blocks with attributes, using Runme's syntax so the same file stays runnable in Runme:

~~~markdown
## Diagnose

```sh {"name":"disk-usage","drill":"check","target":"node"}
df -h /var/lib/containerd
```

## Fix

Prune unused images and shrink the journal:

```sh {"name":"prune","drill":"fix","target":"node"}
crictl rmi --prune
journalctl --vacuum-size=200M
```
~~~

Attributes drillbook reads:

- `drill`: `check` or `fix`. Untagged blocks are ignored.
- `target`: `node` (the drill's target host, over Ansible), `runner` (where drillbook runs, for `kubectl` and similar), or `host:<inventory-name>`.
- `timeout`: per-block limit, default 5m.

Before relying on Runme compatibility, confirm Runme ignores attribute keys it does not know.

## Drill file

```yaml
# drills/node-disk-full.yaml
alert: NodeFilesystemAlmostOutOfSpace
runbook: runbooks/node/NodeFilesystemAlmostOutOfSpace.md
target:
  host: worker-1                # Ansible inventory name
  labels: {instance: "worker-1:9100"}
fault:
  role: drillbook.faults.fill_filesystem
  vars: {path: /var/lib/containerd, leave_free_percent: 3}
  revert_after: 90m
fire_within: 45m
resolve_within: 15m
expected_resolve: 10m           # optional, from the runbook's own claim
expect:
  receiver: platform-oncall     # optional routing check
schedule: weekly                # used by the scheduled lane
```

## Components

### `drillbook` CLI (Go)

| Command | Purpose |
|---|---|
| `drillbook run <drill...>` | Run drills, print verdicts, exit non-zero on any failure |
| `drillbook plan <drill>` | Dry run: show the fault, the silence, and the runbook blocks it would execute |
| `drillbook lint` | Every alert rule has a runbook, every runbook has a `fix` block, every drill file is valid |
| `drillbook affected <git-range>` | List drills whose runbook, rule or drill file changed |
| `drillbook report` | Write JSON and markdown summaries of recent results |
| `drillbook annotate <runbook>` | LLM proposes `drill` tags for existing blocks, as a diff for review |

Internal packages, each testable on its own:

- `drill`: load and validate drill files.
- `runbook`: parse markdown and extract tagged blocks.
- `prom`: query `ALERTS`, using the Prometheus Go client.
- `am`: silences and route matching, using the Alertmanager Go client and its routing code.
- `exec/ansible`: run fault roles and host blocks by calling `ansible-playbook` with a generated playbook.
- `exec/kube`: cluster-side faults (crashing pod, scale to zero, cordon) with client-go.
- `state`: results history in a local SQLite file, exported as metrics.
- `notify`: GitHub issues on failure, closed again on the next pass.

### `drillbook.faults` Ansible collection

Each fault is a role with `apply` and `revert` tasks wrapped in `block`/`always`, and it arms the `systemd-run` revert before applying. Initial set:

| Fault | What it does |
|---|---|
| `stop_service` | Stops a systemd unit (kubelet, containerd, node_exporter, a compose service's unit) |
| `fill_filesystem` | Fills a path to a set free-space level with `fallocate` |
| `skew_clock` | Stops time sync and shifts the clock |
| `drop_traffic` | Adds an nftables rule dropping traffic to a port or peer |
| `expire_cert` | Swaps in a short-lived certificate on a test component |
| `stop_container` | Stops a Docker container (for hosts like Private-Cloud) |

### Drill environment (Terraform + Ansible)

- Terraform with the libvirt provider creates the VMs. An AWS variant of the same module comes in v0.3.
- Ansible playbooks install containerd and kubeadm, create the cluster, join workers, and upgrade the cluster one minor version.
- Helm installs kube-prometheus-stack. From v0.2, Argo CD keeps the cluster's baseline in sync with git.
- The v0.1 size is one control plane and one worker, which fits the local machine (14 GB RAM, KVM available, libvirt still to install). Larger runs go to the lab machines only with the lab's permission, or to AWS.

The same environment is drillbook's own end-to-end test bed.

### Metrics and dashboard

drillbook exposes, through a textfile for node_exporter or a small `/metrics` endpoint:

- `drillbook_drill_last_run_timestamp_seconds{drill}`
- `drillbook_drill_last_success_timestamp_seconds{drill, alert}`
- `drillbook_drill_verdict{drill, verdict}` (1 for the latest verdict)
- `drillbook_drill_resolve_seconds{drill}`

It ships alert rules for itself, taken from the Private-Cloud pattern: `DrillbookDrillStale` (no pass within twice the schedule), `DrillbookDrillFailing`, and `DrillbookMetricsMissing` (using `absent()`, so a drill that never ran is not mistaken for one that passes). A Grafana dashboard shows each alert's last pass and verdict history.

### CI

- On pull requests: `promtool check rules`, `promtool test rules`, `pint` against the drill cluster's Prometheus, `drillbook lint`, then `drillbook run $(drillbook affected origin/main...HEAD)` for drills with `fire_within` of 20 minutes or less. Results post as a PR comment.
- On a schedule: every drill, on a self-hosted runner that can reach the drill cluster.
- For drillbook's own code: Go tests, `golangci-lint`, `ansible-lint`, `terraform validate`, and release builds with signed images and binaries.

### LLM use

The LLM helps write and repair runbooks and never decides a verdict.

- `drillbook annotate` proposes `drill` tags for code blocks that already exist in a runbook. A human reviews the diff.
- After a `step-failed` or `not-resolved` verdict, drillbook can ask for a proposed fix to the runbook, rerun the drill against the patched copy, and open a PR only if that rerun passes.

## Safety

- Targets must be listed in an allowlist in `drillbook.yaml`. Production inventories are refused unless a flag names them explicitly.
- Every host fault arms its own revert timer before it is applied.
- Silences are scoped to one alert and one target, with an expiry.
- `drillbook plan` shows exactly what will run. `--dry-run` runs preflight only.
- A kill switch: `drillbook abort` reverts all active faults recorded in state, and deleting a file named in config stops scheduled runs.
- Runbook blocks run with the inventory's normal SSH user, so they run only what an on-call engineer could.

## Testing drillbook itself

- Unit tests for the runbook parser, drill validation, route matching and verdict logic. Verdict logic is tested with fake Prometheus and Alertmanager servers covering every row of the verdict table.
- Molecule tests for each Ansible fault role, including a test that kills the runner mid-drill and checks that the revert timer restores the host.
- End-to-end tests in CI against the drill environment for the first five drills.

## Releases

- v0.1: CLI (`run`, `plan`, `lint`, `affected`), the Ansible collection with `stop_service`, `fill_filesystem`, `skew_clock` and `stop_container`, the libvirt drill environment, five drills with runbooks, the PR lane, and end-to-end CI.
- v0.2: the kube-prometheus-stack drill pack, the scheduled lane, metrics and the Grafana dashboard, GitHub issues, Argo CD for the drill environment, a Helm chart that runs cluster-only drills as a CronJob, and drills for Private-Cloud.
- v0.3: `annotate` and the verified fix loop, upgrade drills (run the pack, upgrade with Ansible, run it again), the AWS drill environment, and `expire_cert` and `drop_traffic`.

## Open questions

- Is Private-Cloud deployed anywhere drillbook can reach, and will its maintainers accept drills in that repository?
- Does Runme tolerate the extra attribute keys?
- Can the lab machines (worker-1 to worker-3, shared `ccbd` account) host drill VMs, or does everything beyond one small cluster go to AWS?
- Which license: Apache-2.0, to match most Kubernetes tooling, is the default.

## Decisions made while building v0.1

The user asked for v0.1 to be built without them, on the laptop only (the lab GPU machines are for GPU work). These changes to the design above were made during the build:

1. Drill nodes are containers, not VMs. There is no sudo and no libvirt on the laptop, so Terraform's Docker provider creates privileged systemd containers from a kind node image with sshd added. Ansible still provisions them over SSH and kubeadm still builds the cluster. Nodes share the host kernel and clock, so `skew_clock` and `expire_cert` are deferred to an environment with real VMs.
2. Results are JSON files, not SQLite. `.drillbook/results.jsonl`, `active/`, `control-cache.json` and `snapshots/` keep the tool free of cgo.
3. Kubernetes faults joined Ansible faults. Three of the five drills are bad deploys, so drills can scale or patch a Deployment. drillbook saves the Deployment's replicas and pod template first and restores them on revert.
4. Routing comes from Alertmanager itself. drillbook reads the `receivers` field of `/api/v2/alerts` (including silenced alerts) instead of re-implementing the routing tree.
5. A ninth verdict, `revert-failed`, ranks just below `aborted`. When drillbook cannot undo its own fault it says so and keeps the active record for `drillbook abort`.
6. Verdict precedence: aborted, revert-failed, alert-did-not-fire, misrouted, step-failed, not-resolved, slower-than-runbook, inconclusive, pass. Every finding is still listed.
7. Drills can pick fix blocks with `fixes: [name, ...]`, because one runbook often covers several causes and a drill injects one of them.
8. **Time to resolve is measured from the first runbook block**, diagnosis included.
9. Role tests are Ansible playbooks run by `tests/roles/run.sh` against a throwaway node container, not Molecule.
10. Revert timers set `AccuracySec=1s`. systemd's default of one minute made a CI test fail when the timer fired late.
11. **`host:<name>` runbook blocks are checked against `allow.hosts`**, the same as fault targets.
12. No operator in v0.1. The CLI runs from CI or an operator's machine, where Ansible inventories and SSH access already live.

