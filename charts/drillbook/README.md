# drillbook Helm chart

Runs drillbook as a CronJob inside the cluster it drills. It works for drills that only need the cluster: Kubernetes faults (`fault.kube`) and runbook blocks with `target: runner`, which run in the drillbook pod with kubectl. Host faults and `node` or `host:` blocks need SSH and Ansible, so run those drills from a machine outside the cluster.

## Install

Put the drill and runbook files in a values file, or pass them with `--set-file`. Inside the pod, drills live in `/work/drills` and runbooks in `/work/runbooks`, so a drill refers to its runbook as `../runbooks/<file>`.

```sh
helm install drillbook charts/drillbook -n drillbook --create-namespace \
  --set 'config.allow.namespaces={shop}' \
  --set-file 'drills.shop-api-scaled-to-zero\.yaml=drills/shop-api-scaled-to-zero.yaml' \
  --set-file 'runbooks.ShopApiDown\.md=examples/shop/runbooks/ShopApiDown.md'
```

To run the drills now instead of waiting for the schedule:

```sh
kubectl -n drillbook create job drill-now --from=cronjob/drillbook
kubectl -n drillbook logs -f job/drill-now
```

## Values

| Value | Default | Meaning |
|---|---|---|
| `image.repository` | `ghcr.io/viviansobers/drillbook` | |
| `image.tag` | chart `appVersion` | |
| `schedule` | `17 4 * * 1` | Cron schedule, UTC |
| `args` | `[run, --all]` | drillbook arguments; name drills to run a subset |
| `config` | kube-prometheus-stack service URLs | Contents of `drillbook.yaml`; `kubeconfig` is left out so the pod uses its service account |
| `config.allow.namespaces` | `[]` | Namespaces drills may touch; the chart creates a Role and RoleBinding in each |
| `drills` | `{}` | Drill files keyed by file name |
| `runbooks` | `{}` | Runbook files keyed by file name |
| `rbac.create` | `true` | Create the per-namespace Roles |
| `githubIssues.repo` | `""` | `owner/name` to open and close issues for failing drills |
| `githubIssues.tokenSecret` | `""` | Secret with the GitHub token under the key `token` |
| `resources` | 50m CPU, 64Mi request, 256Mi limit | |

The default Prometheus and Alertmanager URLs match a kube-prometheus-stack release named `kps` in the `monitoring` namespace. Change `config.prometheus` and `config.alertmanager` for anything else.

## Permissions

In each allowed namespace the service account can get, list, watch, update and patch Deployments, their scale subresource and ReplicaSets, and read pods, pod logs and events. Runbook blocks that need more, such as `kubectl delete pod`, fail with a Kubernetes permission error and the drill reports `step-failed`. Add a Role of your own for those.

## Limits

- The state directory is an `emptyDir`, so results and the control-run cache are gone when the job ends. Every scheduled run repeats the control run after a pass, and `drillbook report` has no history to read. Use the job logs, or `--github-issues`, to follow results.
- The pod runs as a non-root user with a read-only root filesystem and no added capabilities.

## Testing

[tests/chart/run.sh](../../tests/chart/run.sh) installs the chart into the drill environment from `env/up.sh`, runs the `shop-api-scaled-to-zero` drill as a job and fails unless the job succeeds.
