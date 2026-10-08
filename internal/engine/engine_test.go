package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VivianSobers/drillbook/internal/config"
	"github.com/VivianSobers/drillbook/internal/drill"
	"github.com/VivianSobers/drillbook/internal/runbook"
	"github.com/VivianSobers/drillbook/internal/state"
)

// world simulates a system with one fault and one alert. The alert fires
// fireDelay after the fault is applied and stops when the fault is gone.
type world struct {
	now        time.Time
	faultOn    bool
	faultSince time.Time
	fireDelay  time.Duration
	// healAfter > 0 makes the fault disappear on its own after that long.
	healAfter    time.Duration
	alreadyFired bool

	applies, reverts int
	applyErr         error
	failBlock        string // name of a block that exits non-zero
	fixWorks         bool
	blocksRun        []string

	silences   map[string]bool
	nextSil    int
	receivers  []string
	sleeps     int
	cancelAt   int // cancel the context on this many sleeps
	cancel     context.CancelFunc
	promErr    error
	silenceErr error
}

func (w *world) firing() bool {
	if w.alreadyFired {
		return true
	}
	if !w.faultOn {
		return false
	}
	if w.healAfter > 0 && w.now.Sub(w.faultSince) >= w.healAfter {
		w.faultOn = false
		return false
	}
	return w.now.Sub(w.faultSince) >= w.fireDelay
}

// Clock
func (w *world) Now() time.Time { return w.now }
func (w *world) Sleep(ctx context.Context, d time.Duration) error {
	w.sleeps++
	if w.cancelAt > 0 && w.sleeps == w.cancelAt {
		w.cancel()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.now = w.now.Add(d)
	return nil
}

// Prom
type wprom struct{ w *world }

func (p wprom) Firing(ctx context.Context, alert string, labels map[string]string) (bool, error) {
	if p.w.promErr != nil {
		return false, p.w.promErr
	}
	return p.w.firing(), nil
}

// Alertmanager
type wam struct{ w *world }

func (a wam) CreateSilence(ctx context.Context, alert string, labels map[string]string, until time.Time, by, comment string) (string, error) {
	if a.w.silenceErr != nil {
		return "", a.w.silenceErr
	}
	a.w.nextSil++
	id := "s" + string(rune('0'+a.w.nextSil))
	a.w.silences[id] = true
	return id, nil
}
func (a wam) DeleteSilence(ctx context.Context, id string) error {
	delete(a.w.silences, id)
	return nil
}
func (a wam) Receivers(ctx context.Context, alert string, labels map[string]string) ([]string, bool, error) {
	if !a.w.firing() {
		return nil, false, nil
	}
	return a.w.receivers, true, nil
}

// FaultExecutor
type wfault struct{ w *world }

func (f wfault) Apply(ctx context.Context, d *drill.Drill, id string, log io.Writer) error {
	f.w.applies++
	if f.w.applyErr != nil {
		return f.w.applyErr
	}
	f.w.faultOn = true
	f.w.faultSince = f.w.now
	return nil
}
func (f wfault) Revert(ctx context.Context, d *drill.Drill, id string, log io.Writer) error {
	f.w.reverts++
	f.w.faultOn = false
	return nil
}

// BlockRunner
type wblocks struct{ w *world }

func (b wblocks) RunBlock(ctx context.Context, blk runbook.Block, d *drill.Drill, log io.Writer) error {
	b.w.blocksRun = append(b.w.blocksRun, blk.Name)
	if blk.Name == b.w.failBlock {
		return errors.New("exit status 1")
	}
	if blk.Kind == runbook.Fix && b.w.fixWorks {
		b.w.faultOn = false
	}
	b.w.now = b.w.now.Add(30 * time.Second)
	return nil
}

func testDrill(t *testing.T) *drill.Drill {
	t.Helper()
	return &drill.Drill{
		Name:          "kubelet-stopped",
		Alert:         "KubeNodeNotReady",
		File:          "/drills/kubelet-stopped.yaml",
		Hash:          "hash1",
		Target:        drill.Target{Host: "worker", Labels: map[string]string{"node": "worker"}},
		FireWithin:    config.Duration{Duration: 20 * time.Minute},
		ResolveWithin: config.Duration{Duration: 10 * time.Minute},
		Expect:        drill.Expect{Receiver: "platform"},
	}
}

func testRunbook() *runbook.Runbook {
	return &runbook.Runbook{Blocks: []runbook.Block{
		{Name: "status", Kind: runbook.Check},
		{Name: "restart", Kind: runbook.Fix},
		{Name: "verify", Kind: runbook.Fix},
	}}
}

func setup(t *testing.T) (*world, *Engine, *state.Store) {
	t.Helper()
	w := &world{
		now:       time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC),
		fireDelay: 15 * time.Minute,
		fixWorks:  true,
		silences:  map[string]bool{},
		receivers: []string{"platform"},
	}
	st := &state.Store{Dir: t.TempDir()}
	e := &Engine{
		Prom: wprom{w}, AM: wam{w}, Faults: wfault{w}, Blocks: wblocks{w}, Clock: w,
		State: st, Poll: 30 * time.Second, SilenceCreator: "drillbook", Out: &bytes.Buffer{},
	}
	return w, e, st
}

func assertCleanedUp(t *testing.T, w *world, st *state.Store) {
	t.Helper()
	if w.faultOn {
		t.Error("fault still applied after the drill")
	}
	if len(w.silences) != 0 {
		t.Errorf("silences left behind: %v", w.silences)
	}
	if a, _ := st.Active(); len(a) != 0 {
		t.Errorf("active records left behind: %v", a)
	}
}

func TestRunPass(t *testing.T) {
	w, e, st := setup(t)
	r := e.Run(context.Background(), testDrill(t), testRunbook(), RunOptions{})
	if r.Verdict != Pass {
		t.Fatalf("verdict %s findings %v", r.Verdict, r.Findings)
	}
	if strings.Join(w.blocksRun, ",") != "status,restart,verify" {
		t.Errorf("blocks run = %v", w.blocksRun)
	}
	if r.FiredAfter.Duration < 15*time.Minute || r.FiredAfter.Duration > 16*time.Minute {
		t.Errorf("fired after %v", r.FiredAfter)
	}
	if !r.ControlRan || r.ControlCleared {
		t.Errorf("control ran=%v cleared=%v", r.ControlRan, r.ControlCleared)
	}
	if w.applies != 2 {
		t.Errorf("want 2 applies (drill + control), got %d", w.applies)
	}
	if c, ok := st.Control("hash1"); !ok || c {
		t.Errorf("control cache = %v %v", c, ok)
	}
	assertCleanedUp(t, w, st)
	b, err := os.ReadFile(filepath.Join(st.Dir, "results.jsonl"))
	if err != nil || !strings.Contains(string(b), `"verdict":"pass"`) {
		t.Errorf("results.jsonl = %s, %v", b, err)
	}
}

func TestRunAlertDidNotFire(t *testing.T) {
	w, e, st := setup(t)
	w.fireDelay = time.Hour
	r := e.Run(context.Background(), testDrill(t), testRunbook(), RunOptions{})
	if r.Verdict != AlertDidNotFire {
		t.Fatalf("verdict %s", r.Verdict)
	}
	if len(w.blocksRun) != 0 {
		t.Errorf("no runbook steps when the alert never fired, ran %v", w.blocksRun)
	}
	assertCleanedUp(t, w, st)
}

func TestRunCheckFailureStopsBeforeFixes(t *testing.T) {
	w, e, st := setup(t)
	w.failBlock = "status"
	r := e.Run(context.Background(), testDrill(t), testRunbook(), RunOptions{})
	if r.Verdict != StepFailed {
		t.Fatalf("verdict %s", r.Verdict)
	}
	if strings.Join(w.blocksRun, ",") != "status" {
		t.Errorf("blocks run = %v", w.blocksRun)
	}
	if r.ControlRan {
		t.Error("no control run after a failure")
	}
	assertCleanedUp(t, w, st)
}

func TestRunNotResolved(t *testing.T) {
	w, e, st := setup(t)
	w.fixWorks = false
	r := e.Run(context.Background(), testDrill(t), testRunbook(), RunOptions{})
	if r.Verdict != NotResolved {
		t.Fatalf("verdict %s", r.Verdict)
	}
	assertCleanedUp(t, w, st)
}

func TestRunMisrouted(t *testing.T) {
	w, e, _ := setup(t)
	w.receivers = []string{"null"}
	r := e.Run(context.Background(), testDrill(t), testRunbook(), RunOptions{})
	if r.Verdict != Misrouted {
		t.Fatalf("verdict %s", r.Verdict)
	}
	if strings.Join(r.Receivers, ",") != "null" {
		t.Errorf("receivers = %v", r.Receivers)
	}
}

func TestRunAlreadyFiringAbortsWithoutApplying(t *testing.T) {
	w, e, st := setup(t)
	w.alreadyFired = true
	r := e.Run(context.Background(), testDrill(t), testRunbook(), RunOptions{})
	if r.Verdict != Aborted || !strings.Contains(strings.Join(r.Findings, ""), "already firing") {
		t.Fatalf("verdict %s findings %v", r.Verdict, r.Findings)
	}
	if w.applies != 0 {
		t.Errorf("applied %d faults", w.applies)
	}
	if len(w.silences) != 0 {
		t.Error("silence created before preflight passed")
	}
	if a, _ := st.Active(); len(a) != 0 {
		t.Errorf("active = %v", a)
	}
}

func TestRunNoFixBlocksAborts(t *testing.T) {
	w, e, _ := setup(t)
	rb := &runbook.Runbook{Blocks: []runbook.Block{{Name: "status", Kind: runbook.Check}}}
	r := e.Run(context.Background(), testDrill(t), rb, RunOptions{})
	if r.Verdict != Aborted || w.applies != 0 {
		t.Fatalf("verdict %s applies %d", r.Verdict, w.applies)
	}
}

func TestRunInterruptedStillCleansUp(t *testing.T) {
	w, e, st := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.cancelAt = 5 // during the wait for the alert
	r := e.Run(ctx, testDrill(t), testRunbook(), RunOptions{})
	if r.Verdict != Aborted || !strings.Contains(strings.Join(r.Findings, ""), "interrupted") {
		t.Fatalf("verdict %s findings %v", r.Verdict, r.Findings)
	}
	if w.reverts == 0 {
		t.Error("fault not reverted after interrupt")
	}
	assertCleanedUp(t, w, st)
}

func TestRunApplyErrorAbortsAndReverts(t *testing.T) {
	w, e, st := setup(t)
	w.applyErr = errors.New("ssh: connection refused")
	r := e.Run(context.Background(), testDrill(t), testRunbook(), RunOptions{})
	if r.Verdict != Aborted || !strings.Contains(strings.Join(r.Findings, ""), "connection refused") {
		t.Fatalf("verdict %s findings %v", r.Verdict, r.Findings)
	}
	if w.reverts == 0 {
		t.Error("a partly applied fault must still be reverted")
	}
	assertCleanedUp(t, w, st)
}

func TestRunSilenceErrorAbortsBeforeFault(t *testing.T) {
	w, e, _ := setup(t)
	w.silenceErr = errors.New("am down")
	r := e.Run(context.Background(), testDrill(t), testRunbook(), RunOptions{})
	if r.Verdict != Aborted || w.applies != 0 {
		t.Fatalf("verdict %s applies %d", r.Verdict, w.applies)
	}
}

func TestRunControlClearsGivesInconclusive(t *testing.T) {
	w, e, st := setup(t)
	w.healAfter = 18 * time.Minute // fires at 15m, heals itself at 18m
	r := e.Run(context.Background(), testDrill(t), testRunbook(), RunOptions{})
	if r.Verdict != Inconclusive {
		t.Fatalf("verdict %s findings %v", r.Verdict, r.Findings)
	}
	assertCleanedUp(t, w, st)
}

func TestRunUsesCachedControl(t *testing.T) {
	w, e, st := setup(t)
	if err := st.SaveControl("hash1", false); err != nil {
		t.Fatal(err)
	}
	r := e.Run(context.Background(), testDrill(t), testRunbook(), RunOptions{})
	if r.Verdict != Pass || !r.ControlCached {
		t.Fatalf("verdict %s cached %v", r.Verdict, r.ControlCached)
	}
	if w.applies != 1 {
		t.Errorf("cached control must not re-apply the fault, applies = %d", w.applies)
	}
}

func TestRunSkipControl(t *testing.T) {
	w, e, _ := setup(t)
	r := e.Run(context.Background(), testDrill(t), testRunbook(), RunOptions{SkipControl: true})
	if r.Verdict != Pass || r.ControlRan || w.applies != 1 {
		t.Fatalf("verdict %s controlRan %v applies %d", r.Verdict, r.ControlRan, w.applies)
	}
}

func TestRunSlowerThanRunbook(t *testing.T) {
	w, e, _ := setup(t)
	d := testDrill(t)
	d.ExpectedResolve = config.Duration{Duration: 30 * time.Second}
	w.receivers = []string{"platform"}
	r := e.Run(context.Background(), d, testRunbook(), RunOptions{SkipControl: true})
	if r.Verdict != SlowerThanRunbook {
		t.Fatalf("verdict %s resolvedAfter %v", r.Verdict, r.ResolvedAfter)
	}
}

func TestRunPrometheusErrorDuringPreflightAborts(t *testing.T) {
	w, e, _ := setup(t)
	w.promErr = errors.New("connection refused")
	r := e.Run(context.Background(), testDrill(t), testRunbook(), RunOptions{})
	if r.Verdict != Aborted || w.applies != 0 {
		t.Fatalf("verdict %s applies %d", r.Verdict, w.applies)
	}
}

type failingRevert struct{ wfault }

func (f failingRevert) Revert(ctx context.Context, d *drill.Drill, id string, log io.Writer) error {
	f.w.reverts++
	return errors.New("ssh: no route to host")
}

func TestRunRevertFailureIsReportedAndKeepsActiveRecord(t *testing.T) {
	w, e, st := setup(t)
	e.Faults = failingRevert{wfault{w}}
	r := e.Run(context.Background(), testDrill(t), testRunbook(), RunOptions{SkipControl: true})
	if r.Verdict == Pass {
		t.Fatalf("a drill whose fault could not be reverted must not pass, findings %v", r.Findings)
	}
	if !strings.Contains(strings.Join(r.Findings, "\n"), "no route to host") {
		t.Errorf("findings %v must carry the revert error", r.Findings)
	}
	if a, _ := st.Active(); len(a) != 1 {
		t.Errorf("active record must stay so `drillbook abort` can retry, got %v", a)
	}
	if len(w.silences) != 0 {
		t.Errorf("silence should still be deleted, got %v", w.silences)
	}
}
