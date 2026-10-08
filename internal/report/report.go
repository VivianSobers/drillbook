// Package report turns .drillbook/results.jsonl into a markdown summary with
// the latest result of each drill.
package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/VivianSobers/drillbook/internal/engine"
)

// Load reads a results file and keeps the last result of each drill, sorted by drill name.
func Load(path string) ([]engine.Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	latest := map[string]engine.Result{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; sc.Scan(); n++ {
		var r engine.Result
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, n, err)
		}
		latest[r.Drill] = r
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out := make([]engine.Result, 0, len(latest))
	for _, r := range latest {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Drill < out[j].Drill })
	return out, nil
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
