# Runbooks

Runbooks for alerts drillbook drills. They are written for people first: an on-call engineer should be able to follow each one without drillbook.

- [kubernetes/](kubernetes) covers kube-prometheus-stack's Kubernetes alerts.
- [node/](node) covers node-exporter alerts.
- [drillbook/](drillbook) covers drillbook's own alerts on stale or failing drills.

Code blocks tagged with a `drill` attribute are the steps drillbook runs; see "Writing a runbook" in the [README](../README.md). Diagnostic blocks are tagged `check` and must exit 0. Repair blocks are tagged `fix`.

When you edit a runbook, run the drill that uses it. CI does this for drills that fire within 20 minutes.
