// Package config loads drillbook.yaml, the file that says where Prometheus,
// Alertmanager, the inventory and the kubeconfig are, and which targets drills
// may touch.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// Duration is a time.Duration written as "15s" or "20m" in YAML.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"15m\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// Allowlist names the hosts and namespaces drills may target. Empty allows nothing.
type Allowlist struct {
	Hosts      []string `json:"hosts"`
	Namespaces []string `json:"namespaces"`
}

func (a Allowlist) Host(h string) bool      { return slices.Contains(a.Hosts, h) }
func (a Allowlist) Namespace(n string) bool { return slices.Contains(a.Namespaces, n) }

type Ansible struct {
	Playbook        string `json:"playbook"`
	CollectionsPath string `json:"collections_path"`
}

type Config struct {
	Prometheus     string    `json:"prometheus"`
	Alertmanager   string    `json:"alertmanager"`
	Kubeconfig     string    `json:"kubeconfig"`
	Inventory      string    `json:"inventory"`
	Ansible        Ansible   `json:"ansible"`
	StateDir       string    `json:"state_dir"`
	PollInterval   Duration  `json:"poll_interval"`
	SilenceCreator string    `json:"silence_creator"`
	Allow          Allowlist `json:"allow"`
	// Dir is the directory holding the config file; relative paths resolve against it.
	Dir string `json:"-"`
}

// Load reads and validates a config file, applying defaults and resolving
// relative paths against the file's directory.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.UnmarshalStrict(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	c.Dir = filepath.Dir(abs)
	if c.Prometheus == "" || c.Alertmanager == "" {
		return nil, errors.New(path + ": prometheus and alertmanager URLs are required")
	}
	if c.PollInterval.Duration == 0 {
		c.PollInterval.Duration = 15 * time.Second
	}
	if c.SilenceCreator == "" {
		c.SilenceCreator = "drillbook"
	}
	if c.Ansible.Playbook == "" {
		c.Ansible.Playbook = "ansible-playbook"
	}
	if c.StateDir == "" {
		c.StateDir = ".drillbook"
	}
	c.Kubeconfig = c.resolve(c.Kubeconfig)
	c.Inventory = c.resolve(c.Inventory)
	c.Ansible.CollectionsPath = c.resolve(c.Ansible.CollectionsPath)
	c.StateDir = c.resolve(c.StateDir)
	// A playbook path with a slash is a file; a bare name is looked up on PATH.
	if strings.Contains(c.Ansible.Playbook, "/") {
		c.Ansible.Playbook = c.resolve(c.Ansible.Playbook)
	}
	return &c, nil
}

func (c *Config) resolve(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Dir, p)
}
