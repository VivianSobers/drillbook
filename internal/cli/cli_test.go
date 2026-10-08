package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"drillbook.yaml": "prometheus: http://127.0.0.1:1\nalertmanager: http://127.0.0.1:1\nallow:\n  hosts: [worker]\n  namespaces: [shop]\n",
		"drills/kubelet-stopped.yaml": `alert: KubeNodeNotReady
runbook: ../runbooks/KubeNodeNotReady.md
target: {host: worker, labels: {node: worker}, env: {NODE: worker}}
fault:
  ansible: {role: drillbook.faults.stop_service, vars: {stop_service_name: kubelet}}
  revert_after: 45m
fire_within: 20m
resolve_within: 10m
expected_resolve: 5m
expect: {receiver: "null"}
`,
		"runbooks/KubeNodeNotReady.md": "# KubeNodeNotReady\n\n```sh {\"name\":\"node-status\",\"drill\":\"check\"}\nkubectl get node \"$NODE\"\n```\n\n```sh {\"name\":\"restart\",\"drill\":\"fix\",\"target\":\"node\"}\nsystemctl restart kubelet\n```\n",
	}
	for p, body := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func run(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := New(&out)
	cmd.SetArgs(append([]string{"--config", filepath.Join(dir, "drillbook.yaml")}, args...))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	return out.String(), err
}

func TestLintClean(t *testing.T) {
	dir := fixture(t)
	out, err := run(t, dir, "lint")
	if err != nil {
		t.Fatalf("lint failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 drill, no problems") {
		t.Errorf("out = %q", out)
	}
}

func TestLintFailsOnProblems(t *testing.T) {
	dir := fixture(t)
	if err := os.WriteFile(filepath.Join(dir, "runbooks/KubeNodeNotReady.md"), []byte("# nothing to run\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, dir, "lint")
	if err == nil {
		t.Fatalf("lint must fail\n%s", out)
	}
	if !strings.Contains(out, "has no fix blocks") {
		t.Errorf("out = %q", out)
	}
}

func TestPlanDescribesEverythingItWouldDo(t *testing.T) {
	dir := fixture(t)
	out, err := run(t, dir, "plan", "kubelet-stopped")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		`KubeNodeNotReady{node="worker"}`,
		"drillbook.faults.stop_service on worker",
		"stop_service_name=kubelet",
		"revert timer 45m0s",
		"fire within 20m0s",
		"resolve within 10m0s",
		"runbook says 5m0s",
		`receiver "null"`,
		"check  node-status",
		"fix    restart",
		"node",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan output missing %q:\n%s", want, out)
		}
	}
}

func TestPlanUnknownDrill(t *testing.T) {
	dir := fixture(t)
	if _, err := run(t, dir, "plan", "nope"); err == nil || !strings.Contains(err.Error(), `no drill named "nope"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunRefusesInvalidDrillBeforeTouchingAnything(t *testing.T) {
	dir := fixture(t)
	p := filepath.Join(dir, "drills/kubelet-stopped.yaml")
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), "host: worker", "host: prod-db", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, dir, "run", "kubelet-stopped")
	if err == nil || !strings.Contains(out+err.Error(), "not in allow.hosts") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".drillbook", "results.jsonl")); !os.IsNotExist(err) {
		t.Fatal("an invalid drill must not produce a result")
	}
}

func TestAffectedListsNames(t *testing.T) {
	dir := fixture(t)
	g := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	g("init", "-q", "-b", "main")
	g("add", ".")
	g("commit", "-qm", "a")
	if err := os.WriteFile(filepath.Join(dir, "runbooks/KubeNodeNotReady.md"), []byte("# changed\n\n```sh {\"drill\":\"fix\"}\ntrue\n```\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g("commit", "-qam", "b")
	out, err := run(t, dir, "affected", "HEAD~1..HEAD")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.TrimSpace(out) != "kubelet-stopped" {
		t.Errorf("out = %q", out)
	}
	out, _ = run(t, dir, "affected", "HEAD~1..HEAD", "--max-fire-within", "5m")
	if strings.TrimSpace(out) != "" {
		t.Errorf("max-fire-within must drop the 20m drill, got %q", out)
	}
}

func TestAbortWithNothingActive(t *testing.T) {
	dir := fixture(t)
	out, err := run(t, dir, "abort")
	if err != nil || !strings.Contains(out, "no active drills") {
		t.Fatalf("err %v out %q", err, out)
	}
}
