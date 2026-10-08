// Package cli defines the drillbook command line.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/VivianSobers/drillbook/internal/affected"
	"github.com/VivianSobers/drillbook/internal/config"
	"github.com/VivianSobers/drillbook/internal/drill"
	"github.com/VivianSobers/drillbook/internal/engine"
	"github.com/VivianSobers/drillbook/internal/lint"
	"github.com/VivianSobers/drillbook/internal/prom"
	"github.com/VivianSobers/drillbook/internal/runbook"
	"github.com/VivianSobers/drillbook/internal/state"
	"github.com/VivianSobers/drillbook/internal/wire"
)

// Version is set at build time with -ldflags "-X .../internal/cli.Version=v0.1.0".
var Version = "dev"

type app struct {
	out        io.Writer
	configPath string
	drillsDir  string
	cfg        *config.Config
	drills     []*drill.Drill
}

func New(out io.Writer) *cobra.Command {
	a := &app{out: out}
	root := &cobra.Command{
		Use:           "drillbook",
		Short:         "Fire drills for Prometheus alerts and the runbooks they link to",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&a.configPath, "config", "drillbook.yaml", "path to drillbook.yaml")
	root.PersistentFlags().StringVar(&a.drillsDir, "drills", "", "drill directory (default: drills/ next to the config file)")
	root.AddCommand(a.runCmd(), a.planCmd(), a.lintCmd(), a.affectedCmd(), a.abortCmd(), a.versionCmd())
	return root
}

func (a *app) load() error {
	cfg, err := config.Load(a.configPath)
	if err != nil {
		return err
	}
	a.cfg = cfg
	dir := a.drillsDir
	if dir == "" {
		dir = filepath.Join(cfg.Dir, "drills")
	}
	a.drills, err = drill.LoadDir(dir)
	return err
}

func (a *app) find(names []string) ([]*drill.Drill, error) {
	byName := map[string]*drill.Drill{}
	for _, d := range a.drills {
		byName[d.Name] = d
	}
	var out []*drill.Drill
	for _, n := range names {
		d, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("no drill named %q", n)
		}
		out = append(out, d)
	}
	return out, nil
}

func (a *app) runCmd() *cobra.Command {
	var all, skipControl bool
	c := &cobra.Command{
		Use:   "run [drill...]",
		Short: "Run drills against the configured systems",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			ds := a.drills
			if !all {
				if len(args) == 0 {
					return errors.New("name drills to run, or pass --all")
				}
				var err error
				if ds, err = a.find(args); err != nil {
					return err
				}
			}
			if len(ds) == 0 {
				fmt.Fprintln(a.out, "no drills to run")
				return nil
			}
			// Refuse the whole batch if anything is invalid, before touching any system.
			type prepared struct {
				d  *drill.Drill
				rb *runbook.Runbook
			}
			var batch []prepared
			var problems []string
			for _, d := range ds {
				for _, e := range d.Validate(a.cfg) {
					problems = append(problems, e.Error())
				}
				rb, err := runbook.Parse(d.Runbook)
				if err != nil {
					problems = append(problems, err.Error())
					continue
				}
				batch = append(batch, prepared{d, rb})
			}
			if len(problems) > 0 {
				return errors.New("refusing to run:\n  " + strings.Join(problems, "\n  "))
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			eng := wire.Engine(a.cfg, a.out)
			var results []engine.Result
			for _, p := range batch {
				if ctx.Err() != nil {
					break
				}
				results = append(results, eng.Run(ctx, p.d, p.rb, engine.RunOptions{SkipControl: skipControl}))
			}
			a.summary(results)
			for _, r := range results {
				if !r.Verdict.Passed() {
					return fmt.Errorf("%d of %d drills did not pass", countFailed(results), len(results))
				}
			}
			if len(results) < len(batch) {
				return errors.New("interrupted before every drill ran")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&all, "all", false, "run every drill in the drill directory")
	c.Flags().BoolVar(&skipControl, "skip-control", false, "skip the control run after a pass")
	return c
}

func countFailed(rs []engine.Result) int {
	n := 0
	for _, r := range rs {
		if !r.Verdict.Passed() {
			n++
		}
	}
	return n
}

func (a *app) summary(rs []engine.Result) {
	if len(rs) == 0 {
		return
	}
	fmt.Fprintln(a.out)
	tw := tabwriter.NewWriter(a.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "DRILL\tVERDICT\tFIRED AFTER\tRESOLVED AFTER\tCONTROL\tLOG")
	for _, r := range rs {
		control := "-"
		if r.ControlRan {
			control = "held"
			if r.ControlCleared {
				control = "cleared"
			}
			if r.ControlCached {
				control += " (cached)"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Drill, r.Verdict, dur(r.FiredAfter.Duration), dur(r.ResolvedAfter.Duration), control, r.LogPath)
	}
	tw.Flush()
	for _, r := range rs {
		for _, f := range r.Findings {
			fmt.Fprintf(a.out, "%s: %s\n", r.Drill, f)
		}
	}
}

func dur(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return d.Round(time.Second).String()
}

func (a *app) planCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan <drill>",
		Short: "Show what a drill would do, without doing it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			ds, err := a.find(args)
			if err != nil {
				return err
			}
			d := ds[0]
			w := a.out
			fmt.Fprintf(w, "drill %s (%s)\n", d.Name, d.File)
			for _, e := range d.Validate(a.cfg) {
				fmt.Fprintf(w, "  INVALID  %v\n", e)
			}
			fmt.Fprintf(w, "  alert     %s\n", alertWithLabels(d))
			fmt.Fprintf(w, "  preflight abort if %s is already firing for these labels\n", d.Alert)
			window := 2*(d.FireWithin.Duration+d.ResolveWithin.Duration) + 15*time.Minute
			fmt.Fprintf(w, "  silence   %s for at most %v, deleted when the drill ends\n", d.Alert, window)
			switch {
			case d.Fault.Ansible != nil:
				fmt.Fprintf(w, "  fault     ansible role %s on %s (%s), host revert timer %v\n",
					d.Fault.Ansible.Role, d.Target.Host, kv(d.Fault.Ansible.Vars), d.Fault.RevertAfter.Duration)
			case d.Fault.Kube != nil:
				k := d.Fault.Kube
				what := "patch"
				if k.Scale != nil {
					what = fmt.Sprintf("scale to %d", *k.Scale)
				} else if k.Patch != nil {
					what = k.Patch.Type + " patch"
				}
				fmt.Fprintf(w, "  fault     %s deployment %s/%s (snapshot saved first)\n", what, k.Namespace, k.Deployment)
			}
			claim := ""
			if d.ExpectedResolve.Duration > 0 {
				claim = fmt.Sprintf(" (runbook says %v)", d.ExpectedResolve.Duration)
			}
			fmt.Fprintf(w, "  wait      fire within %v, resolve within %v%s\n", d.FireWithin.Duration, d.ResolveWithin.Duration, claim)
			if d.Expect.Receiver != "" {
				fmt.Fprintf(w, "  routing   expect receiver %q\n", d.Expect.Receiver)
			}
			fmt.Fprintf(w, "  runbook   %s\n", d.Runbook)
			rb, err := runbook.Parse(d.Runbook)
			if err != nil {
				fmt.Fprintf(w, "  INVALID  %v\n", err)
				return err
			}
			for _, b := range append(rb.Checks(), rb.Fixes()...) {
				fmt.Fprintf(w, "    %-6s %-24s %-14s line %d\n", b.Kind, b.Name, b.Target, b.Line)
			}
			cached := "no"
			if _, ok := (&state.Store{Dir: a.cfg.StateDir}).Control(d.Hash); ok {
				cached = "yes"
			}
			fmt.Fprintf(w, "  control   after a pass, re-apply the fault without the runbook (cached: %s)\n", cached)
			return nil
		},
	}
}

// alertWithLabels renders KubeNodeNotReady{node="w"}.
func alertWithLabels(d *drill.Drill) string {
	return d.Alert + "{" + strings.Join(prom.Matchers(d.Target.Labels), ",") + "}"
}

func kv(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, " ")
}

func (a *app) lintCmd() *cobra.Command {
	var rules []string
	c := &cobra.Command{
		Use:   "lint",
		Short: "Check drills, runbooks and alert rules without touching any system",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			ps := lint.Drills(a.cfg, a.drills)
			if len(rules) > 0 {
				rp, err := lint.Rules(rules)
				if err != nil {
					return err
				}
				ps = append(ps, rp...)
			}
			for _, p := range ps {
				fmt.Fprintln(a.out, p)
			}
			noun := "drills"
			if len(a.drills) == 1 {
				noun = "drill"
			}
			if len(ps) > 0 {
				return fmt.Errorf("%d problems in %d %s", len(ps), len(a.drills), noun)
			}
			fmt.Fprintf(a.out, "%d %s, no problems\n", len(a.drills), noun)
			return nil
		},
	}
	c.Flags().StringSliceVar(&rules, "rules", nil, "rule files or globs whose alerts must carry runbook_url")
	return c
}

func (a *app) affectedCmd() *cobra.Command {
	var maxFire time.Duration
	c := &cobra.Command{
		Use:   "affected <git-range>",
		Short: "List drills whose drill file, runbook or alert rule changed in a git range",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.load(); err != nil {
				return err
			}
			ds, err := affected.Find(a.cfg.Dir, args[0], a.drills, maxFire)
			if err != nil {
				return err
			}
			for _, d := range ds {
				fmt.Fprintln(a.out, d.Name)
			}
			return nil
		},
	}
	c.Flags().DurationVar(&maxFire, "max-fire-within", 0, "skip drills whose fire_within is longer than this")
	return c
}

func (a *app) abortCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "abort",
		Short: "Revert every fault and delete every silence left by interrupted drills",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(a.configPath)
			if err != nil {
				return err
			}
			st := &state.Store{Dir: cfg.StateDir}
			active, err := st.Active()
			if err != nil {
				return err
			}
			if len(active) == 0 {
				fmt.Fprintln(a.out, "no active drills")
				return nil
			}
			eng := wire.Engine(cfg, a.out)
			ctx := context.Background()
			failed := 0
			for _, act := range active {
				d, err := drill.Load(act.DrillFile)
				if err != nil {
					fmt.Fprintf(a.out, "%s: cannot load %s: %v\n", act.ID, act.DrillFile, err)
					failed++
					continue
				}
				if err := eng.Faults.Revert(ctx, d, act.ID, a.out); err != nil {
					fmt.Fprintf(a.out, "%s: revert failed: %v\n", act.ID, err)
					failed++
					continue
				}
				if act.SilenceID != "" {
					if err := eng.AM.DeleteSilence(ctx, act.SilenceID); err != nil {
						fmt.Fprintf(a.out, "%s: delete silence %s: %v\n", act.ID, act.SilenceID, err)
					}
				}
				if err := st.ClearActive(act.ID); err != nil {
					return err
				}
				fmt.Fprintf(a.out, "%s: reverted\n", act.ID)
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d active drills could not be reverted", failed, len(active))
			}
			return nil
		},
	}
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the drillbook version",
		Run:   func(cmd *cobra.Command, args []string) { fmt.Fprintln(a.out, Version) },
	}
}
