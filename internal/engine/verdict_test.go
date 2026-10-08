package engine

import (
	"strings"
	"testing"
	"time"
)

// passing is a full set of observations for a drill that did everything right.
func passing() Observations {
	return Observations{
		Fired:            true,
		FiredAfter:       2 * time.Minute,
		Receivers:        []string{"platform-oncall"},
		ExpectedReceiver: "platform-oncall",
		Resolved:         true,
		ResolvedAfter:    3 * time.Minute,
		ExpectedResolve:  5 * time.Minute,
		ControlRan:       true,
		ControlCleared:   false,
	}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Observations)
		want    Verdict
		finding string // substring expected among findings, if any
	}{
		{"pass", func(o *Observations) {}, Pass, ""},
		{"pass without control run", func(o *Observations) { o.ControlRan = false }, Pass, ""},
		{"control cleared ignored when control did not run", func(o *Observations) { o.ControlRan = false; o.ControlCleared = true }, Pass, ""},
		{"pass without expected receiver", func(o *Observations) { o.ExpectedReceiver = "" }, Pass, ""},
		{"pass without expected resolve", func(o *Observations) { o.ExpectedResolve = 0; o.ResolvedAfter = time.Hour }, Pass, ""},
		{"aborted", func(o *Observations) { *o = Observations{Aborted: true, AbortReason: "alert already firing"} }, Aborted, "alert already firing"},
		{"aborted wins over everything", func(o *Observations) { o.Aborted = true; o.AbortReason = "interrupted"; o.FailedStep = "restart" }, Aborted, "interrupted"},
		{"revert failed beats everything but abort", func(o *Observations) { o.RevertFailed = "no route to host"; o.FailedStep = "x" }, RevertFailed, "no route to host"},
		{"revert failed after alert did not fire", func(o *Observations) { *o = Observations{RevertFailed: "boom"} }, RevertFailed, "boom"},
		{"alert did not fire", func(o *Observations) { *o = Observations{Fired: false} }, AlertDidNotFire, "did not fire"},
		{"misrouted", func(o *Observations) { o.Receivers = []string{"null"} }, Misrouted, `expected receiver "platform-oncall", got [null]`},
		{"never reached alertmanager", func(o *Observations) { o.Receivers = nil }, Misrouted, "got []"},
		{"step failed", func(o *Observations) {
			o.FailedStep = "restart: exit status 1"
			o.Resolved = false
			o.ControlRan = false
		}, StepFailed, "restart: exit status 1"},
		{"not resolved", func(o *Observations) { o.Resolved = false; o.ControlRan = false }, NotResolved, "still firing"},
		{"slower than runbook", func(o *Observations) { o.ResolvedAfter = 9 * time.Minute }, SlowerThanRunbook, "9m0s, runbook says 5m0s"},
		{"inconclusive", func(o *Observations) { o.ControlCleared = true }, Inconclusive, "cleared without the runbook"},
		{"misrouted beats step failed", func(o *Observations) { o.Receivers = []string{"null"}; o.FailedStep = "restart" }, Misrouted, "restart"},
		{"step failed beats slow", func(o *Observations) { o.FailedStep = "x"; o.ResolvedAfter = time.Hour }, StepFailed, ""},
		{"slow beats inconclusive", func(o *Observations) { o.ResolvedAfter = time.Hour; o.ControlCleared = true }, SlowerThanRunbook, "cleared without the runbook"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := passing()
			c.mutate(&o)
			got, findings := Decide(o)
			if got != c.want {
				t.Fatalf("verdict = %s, want %s (findings %v)", got, c.want, findings)
			}
			if c.finding != "" && !strings.Contains(strings.Join(findings, "\n"), c.finding) {
				t.Errorf("findings %q do not mention %q", findings, c.finding)
			}
			if got == Pass && len(findings) != 0 {
				t.Errorf("a pass must have no findings, got %v", findings)
			}
		})
	}
}

func TestVerdictPassed(t *testing.T) {
	if !Pass.Passed() {
		t.Error("pass must count as passed")
	}
	for _, v := range []Verdict{RevertFailed, Aborted, AlertDidNotFire, Misrouted, StepFailed, NotResolved, SlowerThanRunbook, Inconclusive} {
		if v.Passed() {
			t.Errorf("%s must not count as passed", v)
		}
	}
}
