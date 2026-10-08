# DrillbookMetricsMissing

**Severity:** warning. **Owner:** platform on-call.

Prometheus has no drillbook metrics at all. Without them, a drill that stopped running cannot be told apart from one that keeps passing.

## Diagnose

Check that the textfile is written where node_exporter reads it:

```sh {"name":"textfile","drill":"check"}
ls -l "$TEXTFILE_DIR"/drillbook.prom
```

## Fix

Write the metrics after every scheduled run:

```sh {"name":"write-metrics","drill":"fix"}
drillbook report --format prometheus --output "$TEXTFILE_DIR/drillbook.prom"
```

node_exporter must run with `--collector.textfile.directory` pointing at the same directory.
