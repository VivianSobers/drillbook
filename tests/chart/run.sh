#!/usr/bin/env bash
# Installs the Helm chart into the drill environment (env/up.sh) with the
# shop-api-scaled-to-zero drill, runs one job from the CronJob and fails
# unless the job succeeds.
set -euo pipefail
cd "$(dirname "$0")/../.."
export KUBECONFIG=${KUBECONFIG:-$PWD/env/.generated/kubeconfig}
NS=drillbook
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

docker build -q -t drillbook:dev --build-arg VERSION="$(git describe --tags --always --dirty)" . >/dev/null
docker save drillbook:dev -o "$WORK/image.tar"
# The node containers mount a tmpfs on /tmp, so stream the image instead of
# copying it in.
for node in drillbook-cp drillbook-worker; do
  docker exec -i "$node" ctr -n k8s.io images import - < "$WORK/image.tar" >/dev/null
done

# In the chart, runbooks sit next to drills/ as runbooks/<file>.
sed 's#runbook: .*#runbook: ../runbooks/ShopApiDown.md#' drills/shop-api-scaled-to-zero.yaml > "$WORK/drill.yaml"
helm upgrade --install drillbook charts/drillbook -n "$NS" --create-namespace \
  --set image.repository=docker.io/library/drillbook --set image.tag=dev --set image.pullPolicy=Never \
  --set 'config.allow.namespaces={shop}' \
  --set-file "drills.shop-api-scaled-to-zero\.yaml=$WORK/drill.yaml" \
  --set-file "runbooks.ShopApiDown\.md=examples/shop/runbooks/ShopApiDown.md" \
  --set 'args={run,shop-api-scaled-to-zero,--skip-control}' --wait >/dev/null

kubectl -n "$NS" delete job chart-test --ignore-not-found >/dev/null
kubectl -n "$NS" create job chart-test --from=cronjob/drillbook >/dev/null
deadline=$((SECONDS + 900))
until status=$(kubectl -n "$NS" get job chart-test -o jsonpath='{.status.succeeded}{.status.failed}') && [ -n "$status" ]; do
  [ "$SECONDS" -lt "$deadline" ] || { echo "chart-test job did not finish in 15m" >&2; exit 1; }
  sleep 5
done
kubectl -n "$NS" logs job/chart-test
[ "$(kubectl -n "$NS" get job chart-test -o jsonpath='{.status.succeeded}')" = 1 ]
