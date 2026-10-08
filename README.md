# drillbook

drillbook runs fire drills on Prometheus alerts and the runbooks they link to. A drill breaks something on purpose, checks that the right alert fires, runs the fix written in the runbook, and checks that the alert clears. If any step fails, it opens an issue.

Status: design stage. See [the design spec](docs/superpowers/specs/2026-10-08-drillbook-design.md).
