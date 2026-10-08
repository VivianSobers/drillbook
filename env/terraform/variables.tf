variable "generated_dir" {
  description = "Absolute directory for the SSH key, inventory and kubeconfig."
  type        = string
}

variable "subnet" {
  description = "Docker network for the drill nodes. The control plane gets .10, workers .11 and up."
  type        = string
  default     = "172.31.250.0/24"
}

variable "workers" {
  description = "Number of worker nodes."
  type        = number
  default     = 1
}

variable "app_data_size" {
  description = "Size of the tmpfs mounted at /var/lib/app-data on workers, the filesystem disk drills fill."
  type        = string
  default     = "256m"
}
