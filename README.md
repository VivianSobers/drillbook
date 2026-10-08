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

## Writing a drill

```yaml
# drills/kubelet-stopped.yaml
alert: KubeNodeNotReady
runbook: ../runbooks/kubernetes/KubeNodeNotReady.md
target:
  host: drillbook-worker            # Ansible inventory name
  labels: {node: drillbook-worker}  # must match the alert; also scopes the silence
  env: {NODE: drillbook-worker}     # exported to runbook blocks
fault:
  ansible:
    role: drillbook.faults.stop_service
    vars: {stop_service_name: kubelet}
  revert_after: 50m                 # host-side safety timer
fire_within: 25m
resolve_within: 10m
expected_resolve: 5m                # optional: the runbook's own promise
expect:
  receiver: platform-oncall         # optional routing check
```

A runbook that covers several causes can list them all; `fixes: [scale-up]` makes a drill run only the fix for the cause it injects.

Kubernetes faults change a Deployment the way a bad deploy would. drillbook saves the Deployment's replicas and pod template first and restores them afterwards:

```yaml
fault:
  kube:
    namespace: shop
    deployment: shop-api
    patch:
      type: json                    # json, merge or strategic
      body:
        - {op: add, path: /spec/template/spec/containers/0/args/-, value: --cache-size=512}
```

## Faults

Host faults are in the `drillbook.faults` Ansible collection under [ansible/collections](ansible/collections/ansible_collections/drillbook/faults). Each role arms a `systemd-run` timer on the host before it changes anything, so a runner that crashes mid-drill never leaves a machine broken.

| Fault | What it does |
|---|---|
| `drillbook.faults.stop_service` | Stops a systemd unit. Refuses `ssh` and `sshd`. |
| `drillbook.faults.fill_filesystem` | Fills a filesystem to a set free percentage with one file. Refuses filesystems over 2 GiB by default. |
| `drillbook.faults.stop_container` | Stops a Docker container, for hosts that run services with Docker or Compose. |
| `kube` with `scale` | Scales a Deployment. |
| `kube` with `patch` | Patches a Deployment with a JSON, merge or strategic patch. |

## Commands

| Command | What it does |
|---|---|
| `drillbook run <drill...>` or `--all` | Runs drills, prints a summary, exits 1 unless every drill passed |
| `drillbook plan <drill>` | Shows the fault, silence, waits and runbook blocks without running anything |
| `drillbook lint [--rules glob]` | Validates drills and runbooks; with `--rules`, every alert rule must carry `runbook_url` |
| `drillbook affected <git-range> [--max-fire-within 20m]` | Lists drills whose drill file, runbook or alert rule changed |
| `drillbook abort` | Reverts faults and deletes silences left by interrupted drills |
| `drillbook list` | Lists drills with their alert, fault, schedule and last verdict |
| `drillbook report` | Prints the latest result of each drill as a markdown table |

Results go to `.drillbook/results.jsonl`, and each drill keeps a full log under `.drillbook/logs/`.

## Safety

- Hosts and namespaces must be listed under `allow` in `drillbook.yaml`. Runbook blocks that target `host:<name>` are checked against the same list.
- Every host fault arms its own revert timer before it is applied, and `revert_after` must leave at least 10 minutes past the drill's waits.
- Silences match one alert and the drill's target labels, and expire on their own.
- If a revert fails, the drill reports `revert-failed` and keeps a record, so `drillbook abort` can retry once the target is back.
- `drillbook plan` shows exactly what a run will do.

## The drill environment

`env/up.sh` builds a small on-prem-style cluster on the local Docker daemon:

- Terraform ([env/terraform](env/terraform)) creates privileged systemd containers from a kind node image with sshd added, plus a network, volumes and an SSH key, and writes an Ansible inventory.
- Ansible ([env/ansible](env/ansible)) runs `kubeadm init` and `kubeadm join`, installs the pod network, installs kube-prometheus-stack with Helm, and deploys the shop demo service with its alert rules.
- Alertmanager routes to two webhook receivers, `platform-oncall` and `shop-team`, that point at a closed port. Nobody gets paged, but the routing tree is real.

The nodes share the host's kernel and clock, so clock faults cannot run in this environment.

## CI

- [ci.yml](.github/workflows/ci.yml) runs the Go tests and linters, ansible-lint, the fault role tests against a throwaway node container, `terraform validate`, the `promtool` rule tests, `pint` and `drillbook lint`.
- [e2e.yml](.github/workflows/e2e.yml) builds the drill environment on the GitHub runner, checks the rules against its Prometheus with `pint`, and runs real drills: those affected by a pull request that fire within 20 minutes, or a chosen set on demand and every week.

## Layout

```
cmd/drillbook/           CLI entry point
internal/                engine, verdicts, runbook parser, executors, Prometheus and Alertmanager clients
ansible/collections/     the drillbook.faults collection
drills/                  drill files
runbooks/                runbooks for kube-prometheus-stack alerts
examples/shop/           demo service, its alert rules, promtool tests and runbook
env/                     Terraform and Ansible for the drill environment
tests/roles/             fault role tests
docs/                    design spec, implementation plan, drill results
```

## License

Apache-2.0
