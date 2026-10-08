// Package affected maps the files changed in a git range to the drills they
// can break: the drill file itself, its runbook, or a rules file defining its alert.
package affected

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/VivianSobers/drillbook/internal/drill"
	"github.com/VivianSobers/drillbook/internal/lint"
)

// Find returns the drills affected by changes in rng (for example
// "origin/main...HEAD"), in the order given. A positive maxFire drops
// drills whose fire_within is longer, for quick PR runs.
func Find(repoDir, rng string, ds []*drill.Drill, maxFire time.Duration) ([]*drill.Drill, error) {
	root, err := gitOut(repoDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	root = canon(strings.TrimSpace(root))
	out, err := gitOut(repoDir, "diff", "--name-only", rng)
	if err != nil {
		return nil, err
	}
	changed := map[string]bool{}
	alerts := map[string]bool{}
	for _, rel := range strings.Fields(out) {
		p := filepath.Join(root, rel)
		changed[p] = true
		ext := filepath.Ext(p)
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			continue // deleted in the range
		}
		rules, err := lint.AlertRules(p)
		if err != nil {
			continue // not a rules file
		}
		for _, r := range rules {
			alerts[r.Alert] = true
		}
	}
	var res []*drill.Drill
	for _, d := range ds {
		if maxFire > 0 && d.FireWithin.Duration > maxFire {
			continue
		}
		if changed[canon(d.File)] || changed[canon(d.Runbook)] || alerts[d.Alert] {
			res = append(res, d)
		}
	}
	return res, nil
}

func canon(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(b)))
	}
	return string(b), nil
}
