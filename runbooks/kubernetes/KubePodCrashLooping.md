# KubePodCrashLooping

**Severity:** warning. **Owner:** the team that owns the namespace.

A container has been restarting for 15 minutes. Set `NAMESPACE` and `DEPLOYMENT` from the alert's labels.

## Diagnose

List the pods and their restart counts:

```sh {"name":"pods","drill":"check"}
kubectl -n "$NAMESPACE" get pods -o wide
```

Read the last output of the crashing container. The previous run's log usually names the reason:

```sh {"name":"crash-log","drill":"check"}
pod=$(kubectl -n "$NAMESPACE" get pods -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.status.containerStatuses[*].state.waiting.reason}{"\n"}{end}' | awk '$2=="CrashLoopBackOff"{print $1; exit}')
if [ -n "$pod" ]; then
  kubectl -n "$NAMESPACE" logs "$pod" --previous --tail=30
else
  echo "no pod is in CrashLoopBackOff right now"
fi
```

See whether a rollout happened just before the crashes started:

```sh {"name":"history","drill":"check"}
kubectl -n "$NAMESPACE" rollout history "deployment/$DEPLOYMENT"
```

## Fix

If the crashes started with the latest rollout (a bad flag, bad config or bad image), roll back and wait for the rollout to finish:

```sh {"name":"rollback","drill":"fix"}
kubectl -n "$NAMESPACE" rollout undo "deployment/$DEPLOYMENT"
kubectl -n "$NAMESPACE" rollout status "deployment/$DEPLOYMENT" --timeout=180s
```

The alert looks back 5 minutes, so it clears about 5 to 6 minutes after the last crash.

## Escalate

If the crashes predate the last rollout, the cause is outside the Deployment (a dependency, a secret, a node). Hand over to the owning team with the log from the diagnose step.
