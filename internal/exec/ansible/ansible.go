// Package ansible applies and reverts host faults and runs runbook blocks on
// hosts by generating a one-play playbook and running ansible-playbook.
package ansible

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/VivianSobers/drillbook/internal/config"
	"github.com/VivianSobers/drillbook/internal/drill"
	"github.com/VivianSobers/drillbook/internal/runbook"
)

type Runner struct {
	Playbook        string
	Inventory       string
	CollectionsPath string
}

func New(cfg *config.Config) *Runner {
	return &Runner{Playbook: cfg.Ansible.Playbook, Inventory: cfg.Inventory, CollectionsPath: cfg.Ansible.CollectionsPath}
}

// Apply runs the role's apply tasks. Roles arm their own host-side revert
// timer for revertAfter before changing anything.
func (r *Runner) Apply(ctx context.Context, f drill.AnsibleFault, host, drillID string, revertAfter time.Duration, log io.Writer) error {
	vars := map[string]any{"drillbook_drill_id": drillID, "drillbook_revert_after": int(revertAfter.Seconds())}
	return r.role(ctx, f, host, "apply", vars, log)
}

// Revert runs the role's revert tasks, which also cancel the revert timer.
func (r *Runner) Revert(ctx context.Context, f drill.AnsibleFault, host, drillID string, log io.Writer) error {
	return r.role(ctx, f, host, "revert", map[string]any{"drillbook_drill_id": drillID}, log)
}

func (r *Runner) role(ctx context.Context, f drill.AnsibleFault, host, phase string, vars map[string]any, log io.Writer) error {
	maps.Copy(vars, f.Vars)
	task := map[string]any{
		"name":                         phase + " " + f.Role,
		"ansible.builtin.include_role": map[string]any{"name": f.Role, "tasks_from": phase},
		"vars":                         vars,
	}
	return r.run(ctx, host, "drillbook "+phase+" "+f.Role, task, log)
}

// RunBlock runs one runbook block on host under bash strict mode.
func (r *Runner) RunBlock(ctx context.Context, b runbook.Block, host string, env map[string]string, log io.Writer) error {
	task := map[string]any{
		"name":                  b.Name,
		"ansible.builtin.shell": map[string]any{"cmd": "set -euo pipefail\n" + b.Script, "executable": "/bin/bash"},
		"timeout":               int(b.Timeout.Seconds()),
	}
	if len(env) > 0 {
		task["environment"] = env
	}
	return r.run(ctx, host, "drillbook block "+b.Name, task, log)
}

func (r *Runner) run(ctx context.Context, host, name string, task map[string]any, log io.Writer) error {
	if host == "" {
		return errors.New("ansible: target host is empty")
	}
	play := []map[string]any{{"name": name, "hosts": host, "gather_facts": false, "tasks": []any{task}}}
	b, err := yaml.Marshal(play)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "drillbook-*.yml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	var tail bytes.Buffer
	cmd := exec.CommandContext(ctx, r.Playbook, "-i", r.Inventory, f.Name())
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "ANSIBLE_FORCE_COLOR=0", "ANSIBLE_RETRY_FILES_ENABLED=0")
	if r.CollectionsPath != "" {
		cmd.Env = append(cmd.Env, "ANSIBLE_COLLECTIONS_PATH="+r.CollectionsPath)
	}
	cmd.Stdout = io.MultiWriter(log, &tail)
	cmd.Stderr = cmd.Stdout
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s on %s: %w\n%s", name, host, err, lastLines(tail.String(), 15))
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
