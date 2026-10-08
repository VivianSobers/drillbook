# drillbook

drillbook runs fire drills on Prometheus alerts and the runbooks they link to. A drill breaks something on purpose, checks that the right alert fires and reaches the right receiver, runs the fix written in the runbook, and checks that the alert clears. Then it breaks the same thing again without the runbook, to make sure the alert did not just clear on its own.

The runbook file is the test. drillbook runs the commands tagged inside the runbook itself, so when someone edits the runbook, or the system changes under it, the next drill finds out.

This is a real run against the drill environment in this repository:

```
$ drillbook run shop-api-scaled-to-zero
[13:50:40] shop-api-scaled-to-zero: silenced ShopApiDown for this target (silence 17738e84-8d87-4355-b1e1-bea9bc867902)
[13:50:40] shop-api-scaled-to-zero: applying fault
[13:50:40] shop-api-scaled-to-zero: waiting up to 4m0s for ShopApiDown to fire
[13:52:25] shop-api-scaled-to-zero: ShopApiDown fired after 1m45s
[13:52:25] shop-api-scaled-to-zero: alertmanager receivers: [shop-team]
[13:52:25] shop-api-scaled-to-zero: running check block deployment (target runner)
[13:52:25] shop-api-scaled-to-zero: running check block pods (target runner)
[13:52:25] shop-api-scaled-to-zero: running check block events (target runner)
[13:52:25] shop-api-scaled-to-zero: running fix block scale-up (target runner)
[13:52:26] shop-api-scaled-to-zero: waiting up to 4m0s for ShopApiDown to clear
[13:52:56] shop-api-scaled-to-zero: ShopApiDown cleared 31s after the runbook started
[13:52:56] shop-api-scaled-to-zero: reverting fault
[13:52:56] shop-api-scaled-to-zero: control run: re-applying the fault without the runbook
[13:58:56] shop-api-scaled-to-zero: control run: cleared without runbook = false
[13:58:56] shop-api-scaled-to-zero: control run: reverting fault
[13:58:56] shop-api-scaled-to-zero: verdict: pass
```

## Why

Alerts and runbooks fail quietly. A rule can reference a metric that an exporter renamed, so it never fires. An alert can fire and route to nobody. A runbook can name a flag or a namespace that no longer exists, and nobody notices until someone follows it during an incident.

Existing tools check parts of this. `promtool test rules` checks rule logic against made-up series, `pint` checks rules against a live Prometheus, and chaos tools such as LitmusChaos and Krkn inject faults and can query Prometheus while they do. None of them run the runbook and check that it clears the alert. drillbook does that, and uses promtool and pint for the parts they already cover.

## How a drill works

1. Preflight: the drill, runbook and target are valid, and the alert is not already firing.
2. Silence the alert for this target only, so nobody is paged.
3. For a host fault, arm a revert timer on the host. Then inject the fault.
4. Wait for the alert to fire, then read which Alertmanager receiver it reached.
5. Run the runbook's `check` blocks, then its `fix` blocks.
6. Wait for the alert to clear and record how long it took.
7. Revert the fault and delete the silence. This happens on every path, including Ctrl-C.
8. After a pass, run the control: inject the fault again without the runbook. If the alert clears anyway, the verdict is `inconclusive`. The control result is cached until the drill file changes.

| Verdict | Meaning |
|---|---|
| `pass` | The alert fired, reached the expected receiver, the runbook cleared it in time, and the control held |
| `alert-did-not-fire` | The fault was applied but the alert never fired |
| `misrouted` | The alert fired but reached a different receiver |
| `step-failed` | A runbook block exited non-zero |
| `not-resolved` | The runbook ran but the alert was still firing at the deadline |
| `slower-than-runbook` | The alert cleared, but slower than the runbook promises |
| `inconclusive` | The alert also cleared without the runbook |
| `revert-failed` | drillbook could not undo its fault; run `drillbook abort` |
| `aborted` | Preflight failed, Prometheus could not be reached, or the drill was interrupted |

## Quick start

You need Docker (no sudo needed), Go 1.27, Terraform, Helm, kubectl and ansible-core. The drill environment uses about 4 GB of memory.

```sh
python3 -m venv .venv && .venv/bin/pip install ansible-core==2.21.5
export PATH=$PWD/.venv/bin:$PATH

env/up.sh                          # Terraform + Ansible: two-node kubeadm cluster with kube-prometheus-stack
make build                         # bin/drillbook
bin/drillbook lint                 # check drills and runbooks without touching anything
bin/drillbook plan kubelet-stopped # show what a drill would do
bin/drillbook run shop-api-scaled-to-zero
env/down.sh                        # remove the environment
```

## Writing a runbook

Runbooks stay ordinary markdown for people. drillbook only reads fenced code blocks that carry a `drill` attribute, written in [Runme](https://runme.dev)'s attribute syntax so the same file stays runnable in Runme:

~~~markdown
## Diagnose

```sh {"name":"usage","drill":"check","target":"node"}
df -h "$MOUNTPOINT"
```

## Fix

```sh {"name":"delete-rotated-logs","drill":"fix","target":"node"}
find "$MOUNTPOINT" -xdev -type f -name '*.log.[0-9]*' -print -delete
```
~~~

| Attribute | Values |
|---|---|
| `drill` | `check` (diagnostics, must exit 0) or `fix` |
| `target` | `runner` (default, where drillbook runs), `node` (the drill's host, over Ansible) or `host:<inventory-name>` |
| `timeout` | Per block, default `5m` |
| `name` | Used in drill files and logs |

Blocks run under `bash -euo pipefail` with the drill's `target.env` exported, so a runbook can say `"$NODE"` where a person would type the node name.
