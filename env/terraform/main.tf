# The drill environment: privileged systemd containers that behave like small
# on-prem machines. Terraform creates them with sshd and a key; Ansible then
# builds a kubeadm cluster on them (see ../ansible).

locals {
  nodes = merge(
    { "cp" = { ip = cidrhost(var.subnet, 10), role = "control_plane", tmpfs = {} } },
    { for i in range(var.workers) :
      (i == 0 ? "worker" : "worker-${i + 1}") => {
        ip    = cidrhost(var.subnet, 11 + i)
        role  = "workers"
        tmpfs = { "/var/lib/app-data" = "size=${var.app_data_size}" }
      }
    }
  )
}

resource "tls_private_key" "ssh" {
  algorithm = "ED25519"
}

resource "local_sensitive_file" "ssh_key" {
  content         = tls_private_key.ssh.private_key_openssh
  filename        = "${var.generated_dir}/id_ed25519"
  file_permission = "0600"
}

resource "docker_image" "node" {
  name         = "drillbook-node:v1.36.4"
  keep_locally = true
  build {
    context = "${path.module}/../node-image"
  }
  triggers = {
    dockerfile = filesha256("${path.module}/../node-image/Dockerfile")
  }
}

resource "docker_network" "drillbook" {
  name = "drillbook"
  ipam_config {
    subnet  = var.subnet
    gateway = cidrhost(var.subnet, 1)
  }
}

resource "docker_volume" "var" {
  for_each = local.nodes
  name     = "drillbook-${each.key}-var"
}

resource "docker_container" "node" {
  for_each = local.nodes

  name          = "drillbook-${each.key}"
  hostname      = "drillbook-${each.key}"
  image         = docker_image.node.image_id
  privileged    = true
  cgroupns_mode = "private"
  # Docker adds label=disable for privileged containers; listing it keeps plans clean.
  security_opts = ["seccomp=unconfined", "apparmor=unconfined", "label=disable"]
  restart       = "no"
  must_run      = true
  env           = ["container=docker"]
  tmpfs         = merge({ "/tmp" = "", "/run" = "" }, each.value.tmpfs)

  volumes {
    volume_name    = docker_volume.var[each.key].name
    container_path = "/var"
  }
  volumes {
    host_path      = "/lib/modules"
    container_path = "/lib/modules"
    read_only      = true
  }

  networks_advanced {
    name         = docker_network.drillbook.id
    ipv4_address = each.value.ip
  }

  # A rebuilt image tag must not silently replace running cluster nodes.
  # To roll nodes onto a new image, run env/down.sh and env/up.sh.
  lifecycle {
    ignore_changes = [image]
  }

  upload {
    content     = tls_private_key.ssh.public_key_openssh
    file        = "/root/.ssh/authorized_keys"
    permissions = "0600"
  }
}

resource "local_file" "inventory" {
  filename        = "${var.generated_dir}/inventory.ini"
  file_permission = "0644"
  content = templatefile("${path.module}/inventory.tftpl", {
    nodes    = { for k, v in local.nodes : "drillbook-${k}" => v }
    key_file = local_sensitive_file.ssh_key.filename
  })
  depends_on = [docker_container.node]
}
