# ShopApiDown

**Severity:** critical. **Owner:** shop team.

No shop-api instance has passed a scrape for a minute. Customers cannot browse or place orders.

## Diagnose

See how many replicas the Deployment wants and how many are available:

```sh {"name":"deployment","drill":"check"}
kubectl -n shop get deployment shop-api -o wide
```

List the pods and where they run:

```sh {"name":"pods","drill":"check"}
kubectl -n shop get pods -l app=shop-api -o wide
```

Read recent events in the namespace. Scaling, failed pulls and crashes show up here:

```sh {"name":"events","drill":"check"}
kubectl -n shop get events --sort-by=.lastTimestamp | tail -n 20
```

## Fix

Pick the fix that matches what you found.

### The Deployment was scaled to zero

`READY 0/0` with no pods means someone, or a bad automation run, scaled it down. Scale it back to two replicas and wait for them:

```sh {"name":"scale-up","drill":"fix"}
kubectl -n shop scale deployment/shop-api --replicas=2
kubectl -n shop rollout status deployment/shop-api --timeout=120s
```

### A bad rollout replaced the healthy pods

If pods crash or fail to pull their image right after a rollout, roll back to the previous ReplicaSet:

```sh {"name":"rollback","drill":"fix"}
kubectl -n shop rollout undo deployment/shop-api
kubectl -n shop rollout status deployment/shop-api --timeout=180s
```

## Escalate

If neither fix brings the pods back within 10 minutes, page the platform on-call: the cluster itself may be unhealthy.
