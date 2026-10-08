# Drill environment

A two-node Kubernetes cluster built the way an on-prem cluster is: Terraform creates the machines, Ansible configures them over SSH, and kubeadm builds the cluster. The machines are privileged systemd containers on the local Docker daemon, so it runs on a laptop without sudo.

```sh
env/up.sh     # build or update; safe to rerun
env/down.sh   # destroy nodes, volumes and network
```

## What gets built

| Piece | Where | Notes |
|---|---|---|
| Node image | [node-image/Dockerfile](node-image/Dockerfile) | kind's node image (kubeadm, kubelet, containerd) plus sshd and python3 |
| Nodes, network, volumes, SSH key | [terraform/](terraform) | `drillbook-cp` at 172.31.250.10, `drillbook-worker` at .11 with a 256 MiB tmpfs at `/var/lib/app-data` for disk drills |
| Inventory | `.generated/inventory.ini` | Written by Terraform |
| Cluster | [ansible/roles/kubeadm_init](ansible/roles/kubeadm_init), [kubeadm_join](ansible/roles/kubeadm_join) | Kubernetes v1.36.4, kindnet pod network, local-path storage |
| Monitoring | [ansible/roles/monitoring](ansible/roles/monitoring) | kube-prometheus-stack 92.1.1; Prometheus on NodePort 30090, Alertmanager on 30093 |
| Demo service | [ansible/roles/shop_demo](ansible/roles/shop_demo) | [examples/shop](../examples/shop) and its alert rules |

`env/up.sh` returns once Prometheus has scraped the shop service at least once, so rule checks against it are meaningful straight away.

## Things the drills taught us

- **Monitoring runs on the control-plane node.** At first everything ran on the worker. The `kubelet-stopped` drill showed that `KubeNodeNotReady` could then never fire: about a minute after the worker went `NotReady`, the endpoints of Prometheus and kube-state-metrics on it were marked unready, so Prometheus could not be queried and kube-state-metrics was not scraped.
- **The node image tag belongs to Terraform.** The role tests used to build the same tag with BuildKit, which gives a different image ID than Terraform's builder. The next `env/up.sh` then replaced both nodes in the middle of a drill. The role tests now use their own tag, and Terraform ignores image drift on running nodes. To move nodes to a new image, run `env/down.sh` and `env/up.sh`.

## Limits

The nodes share the host's kernel and clock, so clock-skew and certificate-expiry drills need real VMs. The environment uses about 4 GB of memory.
