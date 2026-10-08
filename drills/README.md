# Drills

Each file here is one drill against the environment built by `env/up.sh`.

| Drill | Fault | Alert | Receiver | Runbook |
|---|---|---|---|---|
| `shop-api-scaled-to-zero` | Scale `shop/shop-api` to 0 | `ShopApiDown` | `shop-team` | [ShopApiDown](../examples/shop/runbooks/ShopApiDown.md) |
| `shop-api-crashloop` | Release adds an unknown flag, new pods crash | `KubePodCrashLooping` | `shop-team` | [KubePodCrashLooping](../runbooks/kubernetes/KubePodCrashLooping.md) |
| `shop-api-bad-image` | Release points at an image tag that was never pushed | `KubeDeploymentRolloutStuck` | `shop-team` | [KubeDeploymentRolloutStuck](../runbooks/kubernetes/KubeDeploymentRolloutStuck.md) |
| `kubelet-stopped` | Stop the kubelet on `drillbook-worker` | `KubeNodeNotReady` | `platform-oncall` | [KubeNodeNotReady](../runbooks/kubernetes/KubeNodeNotReady.md) |
| `app-data-disk-full` | Fill `/var/lib/app-data` on the worker to 4% free with a rotated log | `NodeFilesystemAlmostOutOfSpace` | `platform-oncall` | [NodeFilesystemAlmostOutOfSpace](../runbooks/node/NodeFilesystemAlmostOutOfSpace.md) |

Most kube-prometheus-stack alerts hold for 15 to 30 minutes before firing, so these drills take a while: `drillbook plan <drill>` shows the waits. Only `shop-api-scaled-to-zero` is quick enough to run on every pull request.

Run them all with `drillbook run --all`. Later drills in a batch wait for alerts left over from earlier ones to clear before they start.
