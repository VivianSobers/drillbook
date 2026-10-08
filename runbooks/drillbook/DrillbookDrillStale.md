# DrillbookDrillStale

**Severity:** warning. **Owner:** platform on-call.

A drill has not passed in over eight days. Either its schedule stopped running, or it keeps failing. Until it passes again, the alert it covers is unverified.

## Diagnose

List the drills with their last verdict and last run:

```sh {"name":"list","drill":"check"}
drillbook list
```

If the last run is old, the schedule is not running: check the CI schedule or timer that runs `drillbook run`. If the last run is recent and failing, see `DrillbookDrillFailing`.

## Fix

Run the drill by hand and follow up on its verdict:

```sh {"name":"rerun","drill":"fix"}
drillbook run "$DRILL"
```
