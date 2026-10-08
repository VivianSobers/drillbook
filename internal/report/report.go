// Package report turns .drillbook/results.jsonl into a markdown summary with
// the latest result of each drill.
package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/VivianSobers/drillbook/internal/engine"
)

// LoadAll reads every result in a results file, in file order.
func LoadAll(path string) ([]engine.Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var all []engine.Result
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; sc.Scan(); n++ {
		var r engine.Result
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, n, err)
		}
		all = append(all, r)
	}
	return all, sc.Err()
}

// Load reads a results file and keeps the last result of each drill, sorted by drill name.
func Load(path string) ([]engine.Result, error) {
	all, err := LoadAll(path)
	if err != nil {
		return nil, err
	}
	return latest(all), nil
}

func latest(all []engine.Result) []engine.Result {
	byDrill := map[string]engine.Result{}
	for _, r := range all {
		byDrill[r.Drill] = r
	}
	out := make([]engine.Result, 0, len(byDrill))
	for _, r := range byDrill {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Drill < out[j].Drill })
	return out
}

// Prometheus renders results in the text exposition format, for node_exporter's
// textfile collector, so drill freshness can be alerted on like anything else.
func Prometheus(all []engine.Result) string {
	lastPass := map[string]engine.Result{}
	for _, r := range all {
		if r.Verdict.Passed() {
			lastPass[r.Drill] = r
		}
	}
	var b strings.Builder
	metric := func(name, help string, rows func(write func(r engine.Result, v float64))) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
		rows(func(r engine.Result, v float64) {
			fmt.Fprintf(&b, "%s{alert=%s,drill=%s} %s\n", name, quote(r.Alert), quote(r.Drill), strconv.FormatFloat(v, 'f', -1, 64))
		})
	}
	cur := latest(all)
	metric("drillbook_drill_last_run_timestamp_seconds", "Start time of the latest run of the drill.", func(w func(engine.Result, float64)) {
		for _, r := range cur {
			w(r, float64(r.Started.Unix()))
		}
	})
	metric("drillbook_drill_last_success_timestamp_seconds", "Start time of the latest passing run of the drill.", func(w func(engine.Result, float64)) {
		for _, r := range cur {
			if p, ok := lastPass[r.Drill]; ok {
				w(p, float64(p.Started.Unix()))
			}
		}
	})
	metric("drillbook_drill_passed", "1 if the latest run of the drill passed.", func(w func(engine.Result, float64)) {
		for _, r := range cur {
			v := 0.0
			if r.Verdict.Passed() {
				v = 1
			}
			w(r, v)
		}
	})
	metric("drillbook_drill_resolve_seconds", "Time from the first runbook block to the alert clearing, in the latest passing run.", func(w func(engine.Result, float64)) {
		for _, r := range cur {
			if p, ok := lastPass[r.Drill]; ok && p.ResolvedAfter.Duration > 0 {
				w(p, p.ResolvedAfter.Seconds())
			}
		}
	})
	return b.String()
}

func quote(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v) + `"`
}

// Markdown renders results as a table followed by each drill's findings.
func Markdown(rs []engine.Result) string {
	var b strings.Builder
	b.WriteString("| Drill | Alert | Verdict | Fired after | Resolved after | Receivers | Control |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, r := range rs {
		receivers := strings.Join(r.Receivers, ", ")
		if receivers == "" {
			receivers = "-"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			r.Drill, r.Alert, r.Verdict, dur(r.FiredAfter.Duration), dur(r.ResolvedAfter.Duration), receivers, control(r))
	}
	var findings []string
	for _, r := range rs {
		for _, f := range r.Findings {
			findings = append(findings, fmt.Sprintf("- %s: %s", r.Drill, f))
		}
	}
	if len(findings) > 0 {
		b.WriteString("\n" + strings.Join(findings, "\n") + "\n")
	}
	return b.String()
}

func dur(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return d.Round(time.Second).String()
}

func control(r engine.Result) string {
	if !r.ControlRan {
		return "-"
	}
	s := "held"
	if r.ControlCleared {
		s = "cleared without runbook"
	}
	if r.ControlCached {
		s += " (cached)"
	}
	return s
}
