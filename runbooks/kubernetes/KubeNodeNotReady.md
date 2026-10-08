# KubeNodeNotReady

**Severity:** warning. **Owner:** platform on-call.

A node has reported `Ready=False` or `Unknown` for 15 minutes. Pods on it stop getting updates, and after the eviction timeout they are rescheduled elsewhere if there is room. Set `NODE` to the node name from the alert.

## Diagnose

Check the node's status and conditions:

```sh {"name":"node-status","drill":"check"}
kubectl get node "$NODE" -o wide
kubectl describe node "$NODE" | sed -n '/^Conditions:/,/^Addresses:/p'
```

`Unknown` on every condition with the message "Kubelet stopped posting node status" means the kubelet is not running or cannot reach the API server. On the node, check the kubelet:

```sh {"name":"kubelet-state","drill":"check","target":"node"}
systemctl is-active kubelet || journalctl -u kubelet -n 30 --no-pager
```

## Fix

If the kubelet is stopped or failed, restart it and confirm it stays up:

```sh {"name":"restart-kubelet","drill":"fix","target":"node"}
systemctl restart kubelet
sleep 5
systemctl is-active kubelet
```

Then wait for the node to report Ready again:

```sh {"name":"wait-ready","drill":"fix"}
kubectl wait --for=condition=Ready "node/$NODE" --timeout=180s
```

If the kubelet keeps failing, read `journalctl -u kubelet -n 200` for the error. Expired client certificates, a full disk and a stopped container runtime are the usual causes.

## Escalate

If the node stays NotReady after 15 minutes, cordon it (`kubectl cordon "$NODE"`) so nothing new lands there, and open an incident for the hardware or VM owner.
