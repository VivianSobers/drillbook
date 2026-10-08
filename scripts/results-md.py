#!/usr/bin/env python3
"""Render .drillbook/results.jsonl as a markdown table, newest run per drill."""
import json
import re
import sys


def rounded(d):
    """Round a Go duration string like 1m45.059s to whole seconds: 1m45s."""
    if not d or d == "0s":
        return "-"
    secs = 0.0
    for value, unit in re.findall(r"([0-9.]+)(h|m(?!s)|s|ms|µs|us|ns)", d):
        secs += float(value) * {"h": 3600, "m": 60, "s": 1, "ms": 1e-3, "µs": 1e-6, "us": 1e-6, "ns": 1e-9}[unit]
    secs = round(secs)
    h, rest = divmod(secs, 3600)
    m, s = divmod(rest, 60)
    return (f"{h}h" if h else "") + (f"{m}m" if m or h else "") + f"{s}s"


path = sys.argv[1] if len(sys.argv) > 1 else ".drillbook/results.jsonl"
latest = {}
with open(path) as f:
    for line in f:
        r = json.loads(line)
        latest[r["drill"]] = r

print("| Drill | Alert | Verdict | Fired after | Resolved after | Receivers | Control |")
print("|---|---|---|---|---|---|---|")
for name in sorted(latest):
    r = latest[name]
    control = "-"
    if r.get("control_ran"):
        control = "cleared without runbook" if r.get("control_cleared") else "held"
        if r.get("control_cached"):
            control += " (cached)"
    receivers = ", ".join(r.get("receivers") or []) or "-"
    print(f"| {name} | {r['alert']} | {r['verdict']} | {rounded(r['fired_after'])} | {rounded(r['resolved_after'])} | {receivers} | {control} |")
for name in sorted(latest):
    for finding in latest[name].get("findings") or []:
        print(f"\n- {name}: {finding}")
