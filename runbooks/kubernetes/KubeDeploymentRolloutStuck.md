# KubeDeploymentRolloutStuck

**Severity:** warning. **Owner:** the team that owns the namespace.

A Deployment's rollout has stopped making progress: its `Progressing` condition is `False`, usually with reason `ProgressDeadlineExceeded`. Set `NAMESPACE` and `DEPLOYMENT` from the alert's labels.

## Diagnose

Show the Deployment's conditions:

```sh {"name":"conditions","drill":"check"}
kubectl -n "$NAMESPACE" get "deployment/$DEPLOYMENT" -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'
```

Find the new pods that are not becoming ready, and why:

```sh {"name":"waiting-pods","drill":"check"}
kubectl -n "$NAMESPACE" get pods -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.status.containerStatuses[*].state.waiting.reason}{"\n"}{end}'
```

`ImagePullBackOff` or `ErrImagePull` means the new image tag does not exist or cannot be pulled. `CrashLoopBackOff` means the new version starts and dies.

## Fix

Roll back to the last working ReplicaSet and wait for it:

```sh {"name":"rollback","drill":"fix"}
kubectl -n "$NAMESPACE" rollout undo "deployment/$DEPLOYMENT"
kubectl -n "$NAMESPACE" rollout status "deployment/$DEPLOYMENT" --timeout=180s
```

Then fix the image tag or configuration in the release before deploying again.
