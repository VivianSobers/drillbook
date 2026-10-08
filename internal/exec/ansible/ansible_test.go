package ansible

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VivianSobers/drillbook/internal/drill"
	"github.com/VivianSobers/drillbook/internal/runbook"
)

// fakePlaybook writes a stand-in for ansible-playbook that copies the playbook
// it was given and its environment into dir, then exits with code.
func fakePlaybook(t *testing.T, code int) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "ansible-playbook")
	script := "#!/bin/sh\n" +
		"echo \"$@\" > " + dir + "/args\n" +
		"for a; do last=$a; done\n" +
		"cp \"$last\" " + dir + "/playbook.yml\n" +
		"env > " + dir + "/env\n" +
		"echo fake output\n" +
		"exit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func runner(bin string) *Runner {
	return &Runner{Playbook: bin, Inventory: "/inv.ini", CollectionsPath: "/colls"}
}

func TestApplyIncludesRoleWithDrillVars(t *testing.T) {
	bin, dir := fakePlaybook(t, 0)
	f := drill.AnsibleFault{Role: "drillbook.faults.stop_service", Vars: map[string]any{"stop_service_name": "kubelet"}}
	var log bytes.Buffer
	if err := runner(bin).Apply(context.Background(), f, "worker", "kubelet-stopped-20261008T120000", 45*time.Minute, &log); err != nil {
		t.Fatal(err)
	}
	want := `- gather_facts: false
  hosts: worker
  name: drillbook apply drillbook.faults.stop_service
  tasks:
  - ansible.builtin.include_role:
      name: drillbook.faults.stop_service
      tasks_from: apply
    name: apply drillbook.faults.stop_service
    vars:
      drillbook_drill_id: kubelet-stopped-20261008T120000
      drillbook_revert_after: 2700
      stop_service_name: kubelet
`
	if got := read(t, dir+"/playbook.yml"); got != want {
		t.Errorf("playbook\n%s\nwant\n%s", got, want)
	}
	if args := read(t, dir+"/args"); !strings.HasPrefix(args, "-i /inv.ini ") {
		t.Errorf("args = %q", args)
	}
	env := read(t, dir+"/env")
	if !strings.Contains(env, "ANSIBLE_COLLECTIONS_PATH=/colls") || !strings.Contains(env, "ANSIBLE_NOCOLOR=1") {
		t.Errorf("env missing collections path or nocolor:\n%s", env)
	}
	if !strings.Contains(log.String(), "fake output") {
		t.Errorf("log = %q", log.String())
	}
}

func TestRevertUsesRevertTasks(t *testing.T) {
	bin, dir := fakePlaybook(t, 0)
	f := drill.AnsibleFault{Role: "drillbook.faults.fill_filesystem", Vars: map[string]any{"fill_filesystem_path": "/data"}}
	if err := runner(bin).Revert(context.Background(), f, "worker", "d1", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	pb := read(t, dir+"/playbook.yml")
	if !strings.Contains(pb, "tasks_from: revert") || !strings.Contains(pb, "drillbook_drill_id: d1") || !strings.Contains(pb, "fill_filesystem_path: /data") {
		t.Errorf("revert playbook:\n%s", pb)
	}
	if strings.Contains(pb, "drillbook_revert_after") {
		t.Errorf("revert must not arm a timer:\n%s", pb)
	}
}

func TestRunBlockRunsStrictBashWithEnvAndTimeout(t *testing.T) {
	bin, dir := fakePlaybook(t, 0)
	b := runbook.Block{Name: "restart", Kind: runbook.Fix, Target: "node", Lang: "sh", Script: "systemctl restart kubelet\n", Timeout: 90 * time.Second}
	if err := runner(bin).RunBlock(context.Background(), b, "worker", map[string]string{"NODE": "worker"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	want := `- gather_facts: false
  hosts: worker
  name: drillbook block restart
  tasks:
  - ansible.builtin.shell:
      cmd: |
        set -euo pipefail
        systemctl restart kubelet
      executable: /bin/bash
    environment:
      NODE: worker
    name: restart
    timeout: 90
`
	if got := read(t, dir+"/playbook.yml"); got != want {
		t.Errorf("playbook\n%s\nwant\n%s", got, want)
	}
}

func TestNonZeroExitIsErrorWithOutputTail(t *testing.T) {
	bin, _ := fakePlaybook(t, 2)
	err := runner(bin).Apply(context.Background(), drill.AnsibleFault{Role: "r"}, "h", "d", time.Hour, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "exit status 2") || !strings.Contains(err.Error(), "fake output") {
		t.Fatalf("err = %v", err)
	}
}

func TestHostIsRequired(t *testing.T) {
	bin, _ := fakePlaybook(t, 0)
	if err := runner(bin).Apply(context.Background(), drill.AnsibleFault{Role: "r"}, "", "d", time.Hour, &bytes.Buffer{}); err == nil {
		t.Fatal("empty host must be an error, not an implicit 'all'")
	}
}
