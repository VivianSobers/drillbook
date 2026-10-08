// Package engine runs one drill: preflight, silence, fault, wait for the
// alert, run the runbook, wait for resolution, clean up, and a control run.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/VivianSobers/drillbook/internal/config"
	"github.com/VivianSobers/drillbook/internal/drill"
	"github.com/VivianSobers/drillbook/internal/runbook"
	"github.com/VivianSobers/drillbook/internal/state"
)

type Prom interface {
	Firing(ctx context.Context, alert string, labels map[string]string) (bool, error)
}

type Alertmanager interface {
	CreateSilence(ctx context.Context, alert string, labels map[string]string, until time.Time, createdBy, comment string) (string, error)
	DeleteSilence(ctx context.Context, id string) error
	Receivers(ctx context.Context, alert string, labels map[string]string) ([]string, bool, error)
}

// FaultExecutor applies and reverts a drill's fault, whatever its kind.
type FaultExecutor interface {
	Apply(ctx context.Context, d *drill.Drill, drillID string, log io.Writer) error
	Revert(ctx context.Context, d *drill.Drill, drillID string, log io.Writer) error
}

// BlockRunner runs one runbook block on the block's target.
type BlockRunner interface {
	RunBlock(ctx context.Context, b runbook.Block, d *drill.Drill, log io.Writer) error
}

type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }
func (RealClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type Engine struct {
	Prom           Prom
	AM             Alertmanager
	Faults         FaultExecutor
	Blocks         BlockRunner
	Clock          Clock
	State          *state.Store
	Poll           time.Duration
	SilenceCreator string
	// Out receives one-line progress messages for the person running the drill.
	Out io.Writer
}

type RunOptions struct {
	SkipControl bool
}

type Result struct {
	Drill          string          `json:"drill"`
	ID             string          `json:"id"`
	Alert          string          `json:"alert"`
	Verdict        Verdict         `json:"verdict"`
	Findings       []string        `json:"findings,omitempty"`
	Started        time.Time       `json:"started"`
	Finished       time.Time       `json:"finished"`
	FiredAfter     config.Duration `json:"fired_after"`
	ResolvedAfter  config.Duration `json:"resolved_after"`
	Receivers      []string        `json:"receivers,omitempty"`
	ControlRan     bool            `json:"control_ran"`
	ControlCleared bool            `json:"control_cleared"`
	ControlCached  bool            `json:"control_cached,omitempty"`
	LogPath        string          `json:"log"`
}

// receiverWait bounds how long to wait for a firing alert to show up in Alertmanager.
const receiverWait = 2 * time.Minute

// Run executes the drill. It always reverts the fault, deletes the silence
// and records a result, including when ctx is cancelled.
func (e *Engine) Run(ctx context.Context, d *drill.Drill, rb *runbook.Runbook, opts RunOptions) Result {
	start := e.Clock.Now()
	id := d.Name + "-" + start.UTC().Format("20060102T150405Z")
	res := Result{Drill: d.Name, ID: id, Alert: d.Alert, Started: start}
	var obs Observations
	obs.ExpectedReceiver = d.Expect.Receiver
	obs.ExpectedResolve = d.ExpectedResolve.Duration

	logw, logPath, err := e.State.LogFile(id)
	if err != nil {
		return e.finish(res, Observations{Aborted: true, AbortReason: "log file: " + err.Error()})
	}
	defer logw.Close()
	res.LogPath = logPath
	say := func(format string, a ...any) {
		line := fmt.Sprintf("[%s] %s: ", e.Clock.Now().UTC().Format("15:04:05"), d.Name) + fmt.Sprintf(format, a...) + "\n"
		_, _ = io.WriteString(logw, line)
		if e.Out != nil {
			_, _ = io.WriteString(e.Out, line)
		}
	}
	abort := func(reason string) Result {
		say("aborted: %s", reason)
		return e.finish(res, Observations{Aborted: true, AbortReason: reason})
	}

	// Preflight: nothing is changed until these pass.
	fixes, err := rb.SelectFixes(d.Fixes)
	if err != nil {
		return abort(err.Error())
	}
	if len(fixes) == 0 {
		return abort("runbook " + rb.Path + " has no fix blocks")
	}
	firing, err := e.Prom.Firing(ctx, d.Alert, d.Target.Labels)
	if err != nil {
		return abort("preflight: " + err.Error())
	}
	if firing {
		return abort(d.Alert + " is already firing for this target")
	}

	window := 2*(d.FireWithin.Duration+d.ResolveWithin.Duration) + 15*time.Minute
	silence, err := e.AM.CreateSilence(ctx, d.Alert, d.Target.Labels, start.Add(window), e.SilenceCreator, "drillbook drill "+id)
	if err != nil {
		return abort(err.Error())
	}
	say("silenced %s for this target (silence %s)", d.Alert, silence)
	if err := e.State.MarkActive(state.Active{ID: id, DrillFile: d.File, SilenceID: silence, Started: start}); err != nil {
		e.deleteSilence(silence, say)
		return abort("state: " + err.Error())
	}

	obs = e.drive(ctx, d, append(rb.Checks(), fixes...), opts, id, obs, &res, logw, say)

	e.deleteSilence(silence, say)
	if obs.RevertFailed == "" {
		if err := e.State.ClearActive(id); err != nil {
			say("could not clear active record: %v", err)
		}
	} else {
		say("keeping the active record; run `drillbook abort` once the target is reachable")
	}
	res = e.finish(res, obs)
	say("verdict: %s", res.Verdict)
	return res
}

// drive applies the fault and observes the drill. It always reverts the fault
// before returning and reports a failed revert in the observations.
func (e *Engine) drive(ctx context.Context, d *drill.Drill, steps []runbook.Block, opts RunOptions, id string, obs Observations, res *Result, logw io.Writer, say func(string, ...any)) (out Observations) {
	cleanCtx := context.WithoutCancel(ctx)
	reverted := false
	revert := func() {
		if reverted {
			return
		}
		reverted = true
		say("reverting fault")
		if err := e.Faults.Revert(cleanCtx, d, id, logw); err != nil {
			say("REVERT FAILED, the fault may still be applied: %v", err)
			out.RevertFailed = err.Error()
		}
	}
	defer revert()
	aborted := func(reason string) Observations {
		say("aborted: %s", reason)
		return Observations{Aborted: true, AbortReason: reason}
	}

	say("applying fault")
	if err := e.Faults.Apply(ctx, d, id, logw); err != nil {
		return aborted("apply fault: " + err.Error())
	}
	faultAt := e.Clock.Now()

	say("waiting up to %v for %s to fire", d.FireWithin.Duration, d.Alert)
	fired, err := e.waitFor(ctx, d, true, d.FireWithin.Duration)
	if err != nil {
		return aborted(interrupted(err))
	}
	if !fired {
		say("%s did not fire", d.Alert)
		return Observations{Fired: false}
	}
	obs.Fired = true
	obs.FiredAfter = e.Clock.Now().Sub(faultAt)
	res.FiredAfter.Duration = obs.FiredAfter
	say("%s fired after %v", d.Alert, obs.FiredAfter.Round(time.Second))

	obs.Receivers, err = e.receivers(ctx, d)
	if err != nil {
		return aborted(interrupted(err))
	}
	res.Receivers = obs.Receivers
	say("alertmanager receivers: %v", obs.Receivers)

	runbookAt := e.Clock.Now()
	for _, b := range steps {
		say("running %s block %s (target %s)", b.Kind, b.Name, b.Target)
		if err := e.Blocks.RunBlock(ctx, b, d, logw); err != nil {
			if ctx.Err() != nil {
				return aborted(interrupted(ctx.Err()))
			}
			obs.FailedStep = fmt.Sprintf("%s (line %d): %v", b.Name, b.Line, err)
			say("block %s failed: %v", b.Name, err)
			break
		}
	}
	if obs.FailedStep == "" {
		say("waiting up to %v for %s to clear", d.ResolveWithin.Duration, d.Alert)
		stillFiring, err := e.waitFor(ctx, d, false, d.ResolveWithin.Duration)
		if err != nil {
			return aborted(interrupted(err))
		}
		obs.Resolved = !stillFiring
		if obs.Resolved {
			obs.ResolvedAfter = e.Clock.Now().Sub(runbookAt)
			res.ResolvedAfter.Duration = obs.ResolvedAfter
			say("%s cleared %v after the runbook started", d.Alert, obs.ResolvedAfter.Round(time.Second))
		}
	}
	out = obs
	revert()
	if out.RevertFailed != "" {
		return out
	}

	if v, _ := Decide(out); (v == Pass || v == SlowerThanRunbook) && !opts.SkipControl {
		if cleared, ok := e.State.Control(d.Hash); ok {
			out.ControlRan, out.ControlCleared, res.ControlCached = true, cleared, true
			say("control run cached: cleared without runbook = %v", cleared)
			return out
		}
		ran, cleared, rerr, err := e.control(ctx, d, id+"-control", logw, say)
		if rerr != nil {
			out.RevertFailed = rerr.Error()
		}
		if err != nil {
			a := aborted(interrupted(err))
			a.RevertFailed = out.RevertFailed
			return a
		}
		out.ControlRan, out.ControlCleared = ran, cleared
		if ran {
			if err := e.State.SaveControl(d.Hash, cleared); err != nil {
				say("could not cache control result: %v", err)
			}
		}
	}
	return out
}

// control re-applies the fault without running the runbook and reports
// whether the alert cleared on its own.
func (e *Engine) control(ctx context.Context, d *drill.Drill, id string, logw io.Writer, say func(string, ...any)) (ran, cleared bool, revertErr, err error) {
	cleanCtx := context.WithoutCancel(ctx)
	say("control run: re-applying the fault without the runbook")
	if err := e.State.MarkActive(state.Active{ID: id, DrillFile: d.File, Started: e.Clock.Now()}); err != nil {
		return false, false, nil, err
	}
	defer func() {
		say("control run: reverting fault")
		if revertErr = e.Faults.Revert(cleanCtx, d, id, logw); revertErr != nil {
			say("REVERT FAILED after control run: %v", revertErr)
			return
		}
		_ = e.State.ClearActive(id)
	}()
	if err := e.Faults.Apply(ctx, d, id, logw); err != nil {
		say("control run: apply failed: %v", err)
		return false, false, nil, nil
	}
	fired, err := e.waitFor(ctx, d, true, d.FireWithin.Duration)
	if err != nil || !fired {
		if err == nil {
			say("control run: alert did not fire again; no control result")
		}
		return false, false, nil, err
	}
	still, err := e.waitFor(ctx, d, false, d.ResolveWithin.Duration)
	if err != nil {
		return false, false, nil, err
	}
	say("control run: cleared without runbook = %v", !still)
	return true, !still, nil, nil
}

// waitFor polls until the alert's firing state equals want or within passes.
// It returns the last observed state. If Prometheus could not be asked at the
// deadline it returns an error instead: an unreachable Prometheus says nothing
// about the alert.
func (e *Engine) waitFor(ctx context.Context, d *drill.Drill, want bool, within time.Duration) (bool, error) {
	deadline := e.Clock.Now().Add(within)
	for {
		firing, err := e.Prom.Firing(ctx, d.Alert, d.Target.Labels)
		if err == nil && firing == want {
			return firing, nil
		}
		if !e.Clock.Now().Before(deadline) {
			if err != nil {
				return false, fmt.Errorf("prometheus: %w", err)
			}
			return firing, nil
		}
		if err := e.Clock.Sleep(ctx, e.Poll); err != nil {
			return false, err
		}
	}
}

func (e *Engine) receivers(ctx context.Context, d *drill.Drill) ([]string, error) {
	deadline := e.Clock.Now().Add(receiverWait)
	for {
		rs, found, err := e.AM.Receivers(ctx, d.Alert, d.Target.Labels)
		if err == nil && found {
			return rs, nil
		}
		if !e.Clock.Now().Before(deadline) {
			return nil, nil
		}
		if err := e.Clock.Sleep(ctx, e.Poll); err != nil {
			return nil, err
		}
	}
}

func (e *Engine) deleteSilence(id string, say func(string, ...any)) {
	if err := e.AM.DeleteSilence(context.Background(), id); err != nil {
		say("could not delete silence %s: %v", id, err)
	}
}

func (e *Engine) finish(res Result, obs Observations) Result {
	res.Verdict, res.Findings = Decide(obs)
	res.ControlRan, res.ControlCleared = obs.ControlRan, obs.ControlCleared
	res.Finished = e.Clock.Now()
	if err := e.State.AppendResult(res); err != nil && e.Out != nil {
		fmt.Fprintf(e.Out, "could not record result: %v\n", err)
	}
	return res
}

func interrupted(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "interrupted"
	}
	return err.Error()
}
