output "control_plane_ip" {
  value = local.nodes["cp"].ip
}

output "nodes" {
  value = { for k, v in local.nodes : "drillbook-${k}" => v.ip }
}

output "inventory" {
  value = local_file.inventory.filename
}
