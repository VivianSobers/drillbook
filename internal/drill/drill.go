// Package drill loads and validates drill files: which alert to expect, which
// fault to inject on which target, and which runbook should clear it.
package drill

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/VivianSobers/drillbook/internal/config"
)

// namePattern keeps drill names usable in systemd unit names, file names and labels.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// RevertMargin is the minimum slack between the end of a drill's waits and the
// host-side revert timer, so the timer never undoes the fault mid-drill.
const RevertMargin = 10 * time.Minute

type Target struct {
	// Host is the Ansible inventory name that host faults and node blocks run on.
	Host string `json:"host,omitempty"`
	// Labels must all match the alert's labels; they also scope the silence.
	Labels map[string]string `json:"labels"`
	// Env is exported to every runbook block.
	Env map[string]string `json:"env,omitempty"`
}

type AnsibleFault struct {
	Role string         `json:"role"`
	Vars map[string]any `json:"vars,omitempty"`
}

type Patch struct {
	Type string `json:"type"` // json, merge or strategic
	Body any    `json:"body"`
}

type KubeFault struct {
	Namespace  string `json:"namespace"`
	Deployment string `json:"deployment"`
	Scale      *int32 `json:"scale,omitempty"`
	Patch      *Patch `json:"patch,omitempty"`
}

type Fault struct {
	Ansible     *AnsibleFault   `json:"ansible,omitempty"`
	Kube        *KubeFault      `json:"kube,omitempty"`
	RevertAfter config.Duration `json:"revert_after"`
}

type Expect struct {
	Receiver string `json:"receiver,omitempty"`
}

type Drill struct {
	Name            string          `json:"name,omitempty"`
	Alert           string          `json:"alert"`
	Runbook         string          `json:"runbook"`
	Target          Target          `json:"target"`
	Fault           Fault           `json:"fault"`
	FireWithin      config.Duration `json:"fire_within"`
	ResolveWithin   config.Duration `json:"resolve_within"`
	ExpectedResolve config.Duration `json:"expected_resolve"`
	Expect          Expect          `json:"expect"`
	// Fixes names the runbook's fix blocks to run; empty runs all of them.
	Fixes    []string `json:"fixes,omitempty"`
	Schedule string   `json:"schedule,omitempty"`

	// File is the drill file's absolute path; Hash is a digest of its bytes.
	File string `json:"-"`
	Hash string `json:"-"`
}

// Load reads one drill file. The runbook path resolves against the file's directory.
func Load(path string) (*Drill, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d Drill
	if err := yaml.UnmarshalStrict(b, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	d.File = abs
	sum := sha256.Sum256(b)
	d.Hash = hex.EncodeToString(sum[:])
	if d.Name == "" {
		d.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if d.Runbook != "" && !filepath.IsAbs(d.Runbook) {
		d.Runbook = filepath.Join(filepath.Dir(abs), d.Runbook)
	}
	return &d, nil
}

// LoadDir loads every .yaml and .yml file in dir, sorted by name.
func LoadDir(dir string) ([]*Drill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*Drill
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if e.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		d, err := Load(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Validate returns every problem with the drill against cfg's allowlist.
func (d *Drill) Validate(cfg *config.Config) []error {
	var errs []error
	add := func(format string, a ...any) {
		errs = append(errs, fmt.Errorf("%s: "+format, append([]any{d.Name}, a...)...))
	}

	if !namePattern.MatchString(d.Name) {
		add("name %q must be lowercase letters, digits and dashes (it names the host revert timer)", d.Name)
	}
	if d.Alert == "" {
		add("alert is required")
	}
	if d.Runbook == "" {
		add("runbook is required")
	} else if _, err := os.Stat(d.Runbook); err != nil {
		add("runbook %s: %v", d.Runbook, err)
	}
	if len(d.Target.Labels) == 0 {
		add("target.labels must name at least one label, or the silence would match every %s alert", d.Alert)
	}
	if d.FireWithin.Duration <= 0 {
		add("fire_within must be positive")
	}
	if d.ResolveWithin.Duration <= 0 {
		add("resolve_within must be positive")
	}
	if d.Target.Host != "" && !cfg.Allow.Host(d.Target.Host) {
		add("host %q is not in allow.hosts", d.Target.Host)
	}

	switch {
	case (d.Fault.Ansible == nil) == (d.Fault.Kube == nil):
		add("exactly one of fault.ansible or fault.kube is required")
	case d.Fault.Ansible != nil:
		if d.Fault.Ansible.Role == "" {
			add("fault.ansible.role is required")
		}
		if d.Target.Host == "" {
			add("target.host is required for ansible faults")
		}
		need := d.FireWithin.Duration + d.ResolveWithin.Duration + RevertMargin
		if d.Fault.RevertAfter.Duration < need {
			add("revert_after (%v) must be at least fire_within + resolve_within + 10m (%v)", d.Fault.RevertAfter.Duration, need)
		}
	case d.Fault.Kube != nil:
		k := d.Fault.Kube
		if k.Deployment == "" {
			add("fault.kube.deployment is required")
		}
		if !cfg.Allow.Namespace(k.Namespace) {
			add("namespace %q is not in allow.namespaces", k.Namespace)
		}
		if (k.Scale == nil) == (k.Patch == nil) {
			add("exactly one of fault.kube.scale or fault.kube.patch is required")
		}
		if k.Patch != nil && k.Patch.Type != "json" && k.Patch.Type != "merge" && k.Patch.Type != "strategic" {
			add("patch.type %q must be json, merge or strategic", k.Patch.Type)
		}
	}
	return errs
}
