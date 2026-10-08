# pint against the drill environment's Prometheus: catches rules whose series
# do not exist there, before a live drill spends minutes finding out.
prometheus "drill" {
  uri     = "http://172.31.250.10:30090"
  timeout = "30s"
}
