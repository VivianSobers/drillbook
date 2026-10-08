// Package lint checks drills, their runbooks and alert rule files without
// touching any system.
package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"sigs.k8s.io/yaml"

	"github.com/VivianSobers/drillbook/internal/config"
	"github.com/VivianSobers/drillbook/internal/drill"
	"github.com/VivianSobers/drillbook/internal/runbook"
)

type Problem struct {
	File    string
	Message string
}

func (p Problem) String() string { return p.File + ": " + p.Message }

// Drills validates each drill and parses its runbook.
func Drills(cfg *config.Config, ds []*drill.Drill) []Problem {
	var ps []Problem
	for _, d := range ds {
		for _, err := range d.Validate(cfg) {
			ps = append(ps, Problem{d.File, err.Error()})
		}
		if d.Runbook == "" {
			continue
		}
		if _, err := os.Stat(d.Runbook); err != nil {
			continue // already reported by Validate
		}
		rb, err := runbook.Parse(d.Runbook)
		if err != nil {
			ps = append(ps, Problem{d.Runbook, err.Error()})
			continue
		}
		fixes, err := rb.SelectFixes(d.Fixes)
		switch {
		case err != nil:
			ps = append(ps, Problem{d.File, err.Error()})
		case len(fixes) == 0:
			ps = append(ps, Problem{d.Runbook, "runbook has no fix blocks, so drill " + d.Name + " has nothing to run"})
		}
	}
	return ps
}

// AlertRule is one alerting rule from a Prometheus rule file or a PrometheusRule resource.
type AlertRule struct {
	Alert       string
	File        string
	Annotations map[string]string
}

type ruleGroups struct {
	Groups []struct {
		Rules []struct {
			Alert       string            `json:"alert"`
			Annotations map[string]string `json:"annotations"`
		} `json:"rules"`
	} `json:"groups"`
	Spec *struct {
		Groups []struct {
			Rules []struct {
				Alert       string            `json:"alert"`
				Annotations map[string]string `json:"annotations"`
			} `json:"rules"`
		} `json:"groups"`
	} `json:"spec"`
}

// AlertRules reads the alerting rules in one file. Recording rules are skipped.
func AlertRules(path string) ([]AlertRule, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rg ruleGroups
	if err := yaml.Unmarshal(b, &rg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	groups := rg.Groups
	if rg.Spec != nil {
		groups = rg.Spec.Groups
	}
	var out []AlertRule
	for _, g := range groups {
		for _, r := range g.Rules {
			if r.Alert != "" {
				out = append(out, AlertRule{Alert: r.Alert, File: path, Annotations: r.Annotations})
			}
		}
	}
	return out, nil
}

// Expand resolves globs to files, failing on a glob that matches nothing.
func Expand(globs []string) ([]string, error) {
	var files []string
	for _, g := range globs {
		m, err := filepath.Glob(g)
		if err != nil {
			return nil, err
		}
		if len(m) == 0 {
			return nil, fmt.Errorf("rules glob %q matches no files", g)
		}
		files = append(files, m...)
	}
	sort.Strings(files)
	return files, nil
}

// Rules reports alerting rules without a runbook_url annotation.
func Rules(globs []string) ([]Problem, error) {
	files, err := Expand(globs)
	if err != nil {
		return nil, err
	}
	var ps []Problem
	for _, f := range files {
		rs, err := AlertRules(f)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			if r.Annotations["runbook_url"] == "" {
				ps = append(ps, Problem{f, "alert " + r.Alert + " has no runbook_url annotation"})
			}
		}
	}
	return ps, nil
}
