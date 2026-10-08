#!/usr/bin/env bash
# Destroys the drill environment: node containers, their volumes and the network.
set -euo pipefail
cd "$(dirname "$0")"
terraform -chdir=terraform destroy -input=false -auto-approve -var "generated_dir=$PWD/.generated"
rm -f .generated/kubeconfig .generated/inventory.ini .generated/id_ed25519 .generated/shop-prometheusrule.yaml
