package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadResolvesPathsRelativeToConfigFile(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "drillbook.yaml", `
prometheus: http://prom:9090
alertmanager: http://am:9093
kubeconfig: env/kubeconfig
inventory: env/inventory.ini
ansible:
  playbook: ansible-playbook
  collections_path: ansible/collections
state_dir: .drillbook
poll_interval: 5s
allow:
  hosts: [worker]
  namespaces: [shop]
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Prometheus != "http://prom:9090" || c.Alertmanager != "http://am:9093" {
		t.Fatalf("urls not loaded: %+v", c)
	}
	if c.Kubeconfig != filepath.Join(dir, "env/kubeconfig") {
		t.Errorf("kubeconfig = %q", c.Kubeconfig)
	}
	if c.Inventory != filepath.Join(dir, "env/inventory.ini") {
		t.Errorf("inventory = %q", c.Inventory)
	}
	if c.Ansible.CollectionsPath != filepath.Join(dir, "ansible/collections") {
		t.Errorf("collections_path = %q", c.Ansible.CollectionsPath)
	}
	if c.Ansible.Playbook != "ansible-playbook" {
		t.Errorf("a bare command name must stay a PATH lookup, got %q", c.Ansible.Playbook)
	}
	if c.StateDir != filepath.Join(dir, ".drillbook") {
		t.Errorf("state_dir = %q", c.StateDir)
	}
	if c.PollInterval.Duration != 5*time.Second {
		t.Errorf("poll_interval = %v", c.PollInterval)
	}
	if !c.Allow.Host("worker") || c.Allow.Host("prod-db") {
		t.Errorf("host allowlist wrong")
	}
	if !c.Allow.Namespace("shop") || c.Allow.Namespace("payments") {
		t.Errorf("namespace allowlist wrong")
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "drillbook.yaml", "prometheus: http://p\nalertmanager: http://a\n")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.PollInterval.Duration != 15*time.Second {
		t.Errorf("default poll_interval = %v", c.PollInterval)
	}
	if c.SilenceCreator != "drillbook" {
		t.Errorf("default silence_creator = %q", c.SilenceCreator)
	}
	if c.Ansible.Playbook != "ansible-playbook" {
		t.Errorf("default playbook = %q", c.Ansible.Playbook)
	}
	if c.StateDir != filepath.Join(dir, ".drillbook") {
		t.Errorf("default state_dir = %q", c.StateDir)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "drillbook.yaml", "prometheus: http://p\nalertmanager: http://a\nprometheous: typo\n")
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestLoadRequiresURLs(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "drillbook.yaml", "alertmanager: http://a\n")
	if _, err := Load(p); err == nil {
		t.Fatal("expected error when prometheus is missing")
	}
}

func TestEmptyAllowlistAllowsNothing(t *testing.T) {
	var a Allowlist
	if a.Host("anything") || a.Namespace("default") {
		t.Fatal("empty allowlist must allow nothing")
	}
}
