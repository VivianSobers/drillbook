#!/usr/bin/env bash
# Builds the drill environment: Terraform creates the node containers, Ansible
# turns them into a kubeadm cluster with kube-prometheus-stack and the shop app.
# Safe to run again; every step is idempotent.
set -euo pipefail
cd "$(dirname "$0")"
GEN="$PWD/.generated"
PLAYBOOK=${ANSIBLE_PLAYBOOK:-ansible-playbook}
mkdir -p "$GEN"

terraform -chdir=terraform init -input=false >/dev/null
terraform -chdir=terraform apply -input=false -auto-approve -var "generated_dir=$GEN"

export DRILLBOOK_GENERATED_DIR="$GEN" ANSIBLE_CONFIG="$PWD/ansible/ansible.cfg"
"$PLAYBOOK" -i "$GEN/inventory.ini" ansible/site.yml </dev/null

CP=$(terraform -chdir=terraform output -raw control_plane_ip)
cat <<MSG

drill environment ready
  kubeconfig    $GEN/kubeconfig
  inventory     $GEN/inventory.ini
  prometheus    http://$CP:30090
  alertmanager  http://$CP:30093
MSG
