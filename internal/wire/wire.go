// Package wire connects the drill engine to the real executors: Ansible for
// host faults and host blocks, client-go for Deployment faults, and bash on
// the runner for runner blocks.
package wire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/VivianSobers/drillbook/internal/am"
	"github.com/VivianSobers/drillbook/internal/config"
	"github.com/VivianSobers/drillbook/internal/drill"
	"github.com/VivianSobers/drillbook/internal/engine"
	"github.com/VivianSobers/drillbook/internal/exec/ansible"
	"github.com/VivianSobers/drillbook/internal/exec/kube"
	"github.com/VivianSobers/drillbook/internal/exec/local"
	"github.com/VivianSobers/drillbook/internal/prom"
	"github.com/VivianSobers/drillbook/internal/runbook"
	"github.com/VivianSobers/drillbook/internal/state"
)

type AnsibleFaults interface {
	Apply(ctx context.Context, f drill.AnsibleFault, host, drillID string, revertAfter time.Duration, log io.Writer) error
	Revert(ctx context.Context, f drill.AnsibleFault, host, drillID string, log io.Writer) error
}

type KubeFaults interface {
	Apply(ctx context.Context, k drill.KubeFault, drillID string) error
	Revert(ctx context.Context, k drill.KubeFault, drillID string) error
}

type AnsibleBlocks interface {
	RunBlock(ctx context.Context, b runbook.Block, host string, env map[string]string, log io.Writer) error
}

type LocalBlocks interface {
	RunBlock(ctx context.Context, b runbook.Block, env map[string]string, log io.Writer) error
}

// Faults implements engine.FaultExecutor by the drill's fault kind.
type Faults struct {
	Ansible AnsibleFaults
	Kube    KubeFaults
}

func (f *Faults) Apply(ctx context.Context, d *drill.Drill, id string, log io.Writer) error {
	switch {
	case d.Fault.Ansible != nil:
		return f.Ansible.Apply(ctx, *d.Fault.Ansible, d.Target.Host, id, d.Fault.RevertAfter.Duration, log)
	case d.Fault.Kube != nil:
		return f.Kube.Apply(ctx, *d.Fault.Kube, id)
	}
	return errors.New("drill has no fault")
}

func (f *Faults) Revert(ctx context.Context, d *drill.Drill, id string, log io.Writer) error {
	switch {
	case d.Fault.Ansible != nil:
		return f.Ansible.Revert(ctx, *d.Fault.Ansible, d.Target.Host, id, log)
	case d.Fault.Kube != nil:
		return f.Kube.Revert(ctx, *d.Fault.Kube, id)
	}
	return errors.New("drill has no fault")
}

// Blocks implements engine.BlockRunner by the block's target.
type Blocks struct {
	Local   LocalBlocks
	Ansible AnsibleBlocks
	Allow   config.Allowlist
}

func (b *Blocks) RunBlock(ctx context.Context, blk runbook.Block, d *drill.Drill, log io.Writer) error {
	env := d.Target.Env
	switch {
	case blk.Target == "runner":
		return b.Local.RunBlock(ctx, blk, env, log)
	case blk.Target == "node":
		if d.Target.Host == "" {
			return fmt.Errorf("block %s targets the node but the drill has no target.host", blk.Name)
		}
		return b.Ansible.RunBlock(ctx, blk, d.Target.Host, env, log)
	case strings.HasPrefix(blk.Target, "host:"):
		host := strings.TrimPrefix(blk.Target, "host:")
		if !b.Allow.Host(host) {
			return fmt.Errorf("block %s targets host %q, which is not in allow.hosts", blk.Name, host)
		}
		return b.Ansible.RunBlock(ctx, blk, host, env, log)
	}
	return fmt.Errorf("block %s has unknown target %q", blk.Name, blk.Target)
}

// lazyKube builds the Kubernetes client on first use, so drills with only
// host faults work without a kubeconfig.
type lazyKube struct {
	kubeconfig, snapDir string
	once                sync.Once
	f                   *kube.Faults
	err                 error
}

func (l *lazyKube) get() (*kube.Faults, error) {
	l.once.Do(func() {
		if l.kubeconfig == "" {
			l.err = errors.New("kube fault needs kubeconfig in drillbook.yaml")
			return
		}
		l.f, l.err = kube.New(l.kubeconfig, l.snapDir)
	})
	return l.f, l.err
}

func (l *lazyKube) Apply(ctx context.Context, k drill.KubeFault, id string) error {
	f, err := l.get()
	if err != nil {
		return err
	}
	return f.Apply(ctx, k, id)
}

func (l *lazyKube) Revert(ctx context.Context, k drill.KubeFault, id string) error {
	f, err := l.get()
	if err != nil {
		return err
	}
	return f.Revert(ctx, k, id)
}

// Engine builds a drill engine wired to the systems named in cfg.
func Engine(cfg *config.Config, out io.Writer) *engine.Engine {
	st := &state.Store{Dir: cfg.StateDir}
	ar := ansible.New(cfg)
	return &engine.Engine{
		Prom:           prom.New(cfg.Prometheus),
		AM:             am.New(cfg.Alertmanager),
		Faults:         &Faults{Ansible: ar, Kube: &lazyKube{kubeconfig: cfg.Kubeconfig, snapDir: st.SnapshotDir()}},
		Blocks:         &Blocks{Local: &local.Runner{Kubeconfig: cfg.Kubeconfig}, Ansible: ar, Allow: cfg.Allow},
		Clock:          engine.RealClock{},
		State:          st,
		Poll:           cfg.PollInterval.Duration,
		SilenceCreator: cfg.SilenceCreator,
		Out:            out,
	}
}
