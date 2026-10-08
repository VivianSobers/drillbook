# Shop demo service

A stand-in for a team's service in the drill environment: two replicas of a small HTTP API that exposes Prometheus metrics, a ServiceMonitor, and one alert rule owned by the shop team.

- [shop-api.yaml](shop-api.yaml): namespace, Deployment, Service and ServiceMonitor. `progressDeadlineSeconds` is 120 so a stuck rollout is reported within two minutes.
- [rules.yaml](rules.yaml): `ShopApiDown`, which fires when no instance has been up for a minute. It uses `absent()` so it also fires when there are no pods at all, and it carries `team: shop` so Alertmanager routes it to `shop-team`.
- [rules_test.yaml](rules_test.yaml): promtool tests for the rule. They caught that `absent()` of a comparison drops every label, which is why the rule sets `service: shop-api` itself.
- [runbooks/ShopApiDown.md](runbooks/ShopApiDown.md): the runbook, with a fix for each of the two usual causes.
