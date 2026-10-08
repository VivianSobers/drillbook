# DrillbookDrillFailing

**Severity:** warning. **Owner:** whoever owns the drilled alert.

The latest run of a drill did not pass. The alert it drills, or the runbook that alert links to, may not work when it is needed.

## Diagnose

Show the latest result of every drill and read the finding for this one:

```sh {"name":"report","drill":"check"}
drillbook report
```

Each drill keeps its full log, including the output of every runbook block, under `.drillbook/logs/`.

## Fix

What to do depends on the verdict:

- `alert-did-not-fire`: the rule did not see the fault. Check that its metrics exist (`pint lint` against the cluster's Prometheus) and that the exporters it needs are not running on the node the drill broke.
- `misrouted`: fix the Alertmanager route or the alert's labels.
- `step-failed`: a runbook command no longer works. Fix the runbook and rerun the drill.
- `not-resolved` or `slower-than-runbook`: the runbook's fix is wrong or slow. Fix it, or correct the time it promises.
- `inconclusive`: the system heals itself; the alert may be noise, or the drill needs a fault that does not self-heal.
- `revert-failed`: run `drillbook abort` and check the target by hand.

Then rerun the drill: `drillbook run <drill>`.
