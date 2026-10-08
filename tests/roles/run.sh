#!/usr/bin/env bash
# Runs the fault role tests: a throwaway systemd node container for host
# faults, and the local Docker daemon for stop_container.
set -euo pipefail
cd "$(dirname "$0")/../.."
ROOT=$PWD
PLAYBOOK=${ANSIBLE_PLAYBOOK:-ansible-playbook}
NODE=drillbook-roletest
APP=drillbook-roletest-app
WORK=$(mktemp -d)
cleanup() {
  docker rm -f "$NODE" "$APP" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

docker build -q -t drillbook-node:v1.36.4 env/node-image >/dev/null
ssh-keygen -q -t ed25519 -N '' -f "$WORK/id"
docker rm -f "$NODE" "$APP" >/dev/null 2>&1 || true
docker run -d --name "$NODE" --hostname "$NODE" --privileged \
  --security-opt seccomp=unconfined --security-opt apparmor=unconfined \
  --tmpfs /tmp --tmpfs /run --tmpfs /var/lib/app-data:size=64m \
  --volume /lib/modules:/lib/modules:ro --cgroupns=private \
  drillbook-node:v1.36.4 >/dev/null
docker run -d --name "$APP" alpine:3.22 sleep 3600 >/dev/null

until docker exec "$NODE" systemctl is-active ssh >/dev/null 2>&1; do sleep 1; done
docker exec -i "$NODE" sh -c 'cat > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys' < "$WORK/id.pub"
docker exec "$NODE" sh -c 'printf "[Service]\nExecStart=/bin/sleep infinity\n[Install]\nWantedBy=multi-user.target\n" > /etc/systemd/system/drillbook-dummy.service && systemctl daemon-reload && systemctl enable --now drillbook-dummy'
IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$NODE")

cat > "$WORK/inventory.ini" <<INV
[nodes]
roletest ansible_host=$IP ansible_user=root ansible_ssh_private_key_file=$WORK/id ansible_ssh_common_args='-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null' ansible_python_interpreter=/usr/bin/python3

[local]
localhost ansible_connection=local ansible_python_interpreter=$(command -v python3)
INV

export ANSIBLE_COLLECTIONS_PATH="$ROOT/ansible/collections" ANSIBLE_NOCOLOR=1
for t in "${@:-stop_service fill_filesystem stop_container}"; do
  for name in $t; do
    echo "== $name"
    "$PLAYBOOK" -i "$WORK/inventory.ini" "tests/roles/$name.yml" </dev/null
  done
done
echo "all role tests passed"
