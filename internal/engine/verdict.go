package engine

import (
	"fmt"
	"slices"
	"time"
)

type Verdict string

const (
	Pass              Verdict = "pass"
	AlertDidNotFire   Verdict = "alert-did-not-fire"
	Misrouted         Verdict = "misrouted"
	StepFailed        Verdict = "step-failed"
	NotResolved       Verdict = "not-resolved"
	SlowerThanRunbook Verdict = "slower-than-runbook"
	Inconclusive      Verdict = "inconclusive"
	Aborted           Verdict = "aborted"
	// RevertFailed means drillbook could not undo its own fault: the target
	// may still be broken and needs `drillbook abort` or a person.
	RevertFailed Verdict = "revert-failed"
)

func (v Verdict) Passed() bool { return v == Pass }

// Observations is everything a drill saw, collected by the engine and judged by Decide.
type Observations struct {
	Aborted     bool
	AbortReason string
	// RevertFailed holds the error from undoing the fault, if any.
	RevertFailed string

	Fired      bool
	FiredAfter time.Duration
	// Receivers are the Alertmanager receivers the alert was routed to.
	Receivers        []string
	ExpectedReceiver string

	// FailedStep names the runbook block that failed, with its error.
	FailedStep string

	Resolved        bool
	ResolvedAfter   time.Duration
	ExpectedResolve time.Duration

	ControlRan     bool
	ControlCleared bool
}

// Decide turns observations into one verdict plus every finding that applies.
// When several verdicts apply, the most serious wins, in this order:
// aborted, revert-failed, alert-did-not-fire, misrouted, step-failed, not-resolved,
// slower-than-runbook, inconclusive.
func Decide(o Observations) (Verdict, []string) {
	if o.Aborted {
		return Aborted, []string{"aborted: " + o.AbortReason}
	}
	var findings []string
	var verdicts []Verdict
	note := func(v Verdict, f string) {
		verdicts = append(verdicts, v)
		findings = append(findings, f)
	}
	if o.RevertFailed != "" {
		note(RevertFailed, "could not revert the fault, the target may still be broken: "+o.RevertFailed)
	}
	if !o.Fired {
		note(AlertDidNotFire, "the fault was applied but the alert did not fire within fire_within")
		return verdicts[0], findings
	}
	if o.ExpectedReceiver != "" && !slices.Contains(o.Receivers, o.ExpectedReceiver) {
		note(Misrouted, fmt.Sprintf("expected receiver %q, got %v", o.ExpectedReceiver, nonNil(o.Receivers)))
	}
	switch {
	case o.FailedStep != "":
		note(StepFailed, "runbook step failed: "+o.FailedStep)
	case !o.Resolved:
		note(NotResolved, "the runbook ran but the alert was still firing after resolve_within")
	default:
		if o.ExpectedResolve > 0 && o.ResolvedAfter > o.ExpectedResolve {
			note(SlowerThanRunbook, fmt.Sprintf("resolved after %v, runbook says %v", o.ResolvedAfter, o.ExpectedResolve))
		}
		if o.ControlRan && o.ControlCleared {
			note(Inconclusive, "the alert also cleared without the runbook, so this drill cannot show the runbook works")
		}
	}
	if len(verdicts) == 0 {
		return Pass, nil
	}
	return verdicts[0], findings
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
