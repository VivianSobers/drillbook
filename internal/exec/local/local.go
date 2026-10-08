// Package local runs runbook blocks whose target is the runner itself, such
// as kubectl commands, under bash strict mode.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/VivianSobers/drillbook/internal/runbook"
)

type Runner struct {
	// Kubeconfig, when set, is exported as KUBECONFIG to every block.
	Kubeconfig string
}

func (r *Runner) RunBlock(ctx context.Context, b runbook.Block, env map[string]string, log io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, b.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", "set -euo pipefail\n"+b.Script)
	cmd.Env = os.Environ()
	if r.Kubeconfig != "" {
		cmd.Env = append(cmd.Env, "KUBECONFIG="+r.Kubeconfig)
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout, cmd.Stderr = log, log
	// Kill the whole process group so children like `sleep` die with bash.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("block %s timed out after %v", b.Name, b.Timeout)
	}
	if err != nil {
		return fmt.Errorf("block %s: %w", b.Name, err)
	}
	return nil
}
