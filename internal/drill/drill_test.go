package drill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VivianSobers/drillbook/internal/config"
)

const validAnsible = `
alert: KubeNodeNotReady
runbook: ../runbooks/KubeNodeNotReady.md
target:
  host: worker
  labels: {node: worker}
  env: {NODE: worker}
fault:
  ansible:
    role: drillbook.faults.stop_service
    vars: {service: kubelet}
  revert_after: 45m
fire_within: 20m
resolve_within: 10m
expected_resolve: 5m
expect: {receiver: "null"}
schedule: weekly
`

const validKube = `
alert: ShopApiDown
runbook: ../runbooks/ShopApiDown.md
target:
  labels: {job: shop-api}
fault:
  kube:
    namespace: shop
    deployment: shop-api
    scale: 0
fire_within: 3m
resolve_within: 3m
`

// fixture lays out repo/drills/<name>.yaml and repo/runbooks/*.md.
func fixture(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"drills", "runbooks"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, rb := range []string{"KubeNodeNotReady.md", "ShopApiDown.md"} {
		if err := os.WriteFile(filepath.Join(dir, "runbooks", rb), []byte("# rb\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, "drills", name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func cfg() *config.Config {
	return &config.Config{Allow: config.Allowlist{Hosts: []string{"worker"}, Namespaces: []string{"shop"}}}
}

func TestLoadAnsibleDrill(t *testing.T) {
	p := fixture(t, "kubelet-stopped.yaml", validAnsible)
	d, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "kubelet-stopped" {
		t.Errorf("name defaults to file basename, got %q", d.Name)
	}
	if d.Alert != "KubeNodeNotReady" {
		t.Errorf("alert = %q", d.Alert)
	}
	if want := filepath.Join(filepath.Dir(filepath.Dir(p)), "runbooks", "KubeNodeNotReady.md"); d.Runbook != want {
		t.Errorf("runbook resolved to %q, want %q", d.Runbook, want)
	}
	if d.Fault.Ansible == nil || d.Fault.Ansible.Role != "drillbook.faults.stop_service" || d.Fault.Ansible.Vars["service"] != "kubelet" {
		t.Errorf("ansible fault = %+v", d.Fault.Ansible)
	}
	if d.FireWithin.Duration != 20*time.Minute || d.ResolveWithin.Duration != 10*time.Minute ||
		d.ExpectedResolve.Duration != 5*time.Minute || d.Fault.RevertAfter.Duration != 45*time.Minute {
		t.Errorf("durations wrong: %+v", d)
	}
	if d.Target.Labels["node"] != "worker" || d.Target.Env["NODE"] != "worker" || d.Expect.Receiver != "null" {
		t.Errorf("target/expect wrong: %+v %+v", d.Target, d.Expect)
	}
	if errs := d.Validate(cfg()); len(errs) != 0 {
		t.Errorf("valid drill rejected: %v", errs)
	}
}

func TestLoadKubeDrill(t *testing.T) {
	d, err := Load(fixture(t, "scale.yaml", validKube))
	if err != nil {
		t.Fatal(err)
	}
	if d.Fault.Kube == nil || d.Fault.Kube.Scale == nil || *d.Fault.Kube.Scale != 0 {
		t.Fatalf("kube fault = %+v", d.Fault.Kube)
	}
	if errs := d.Validate(cfg()); len(errs) != 0 {
		t.Errorf("valid drill rejected: %v", errs)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	if _, err := Load(fixture(t, "x.yaml", validKube+"fire_withn: 3m\n")); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func validateErrs(t *testing.T, body string) string {
	t.Helper()
	d, err := Load(fixture(t, "x.yaml", body))
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, e := range d.Validate(cfg()) {
		msgs = append(msgs, e.Error())
	}
	return strings.Join(msgs, "; ")
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"both fault kinds", strings.Replace(validKube, "fault:\n", "fault:\n  ansible: {role: drillbook.faults.stop_service}\n", 1), "exactly one of fault.ansible or fault.kube"},
		{"no fault", strings.Replace(validKube, "  kube:\n    namespace: shop\n    deployment: shop-api\n    scale: 0\n", "  {}\n", 1), "exactly one of fault.ansible or fault.kube"},
		{"host outside allowlist", strings.Replace(validAnsible, "host: worker", "host: prod-db", 1), `host "prod-db" is not in allow.hosts`},
		{"ansible fault without host", strings.Replace(validAnsible, "  host: worker\n", "", 1), "target.host is required for ansible faults"},
		{"namespace outside allowlist", strings.Replace(validKube, "namespace: shop", "namespace: payments", 1), `namespace "payments" is not in allow.namespaces`},
		{"revert too soon", strings.Replace(validAnsible, "revert_after: 45m", "revert_after: 35m", 1), "revert_after (35m0s) must be at least fire_within + resolve_within + 10m (40m0s)"},
		{"revert missing", strings.Replace(validAnsible, "  revert_after: 45m\n", "", 1), "revert_after (0s) must be at least"},
		{"missing runbook", strings.Replace(validKube, "ShopApiDown.md", "Nope.md", 1), "runbook"},
		{"no alert", strings.Replace(validKube, "alert: ShopApiDown\n", "", 1), "alert is required"},
		{"no fire_within", strings.Replace(validKube, "fire_within: 3m\n", "", 1), "fire_within must be positive"},
		{"no resolve_within", strings.Replace(validKube, "resolve_within: 3m\n", "", 1), "resolve_within must be positive"},
		{"kube scale and patch", strings.Replace(validKube, "    scale: 0\n", "    scale: 0\n    patch: {type: merge, body: {}}\n", 1), "exactly one of fault.kube.scale or fault.kube.patch"},
		{"bad patch type", strings.Replace(validKube, "    scale: 0\n", "    patch: {type: yaml, body: {}}\n", 1), `patch.type "yaml" must be json, merge or strategic`},
		{"no deployment", strings.Replace(validKube, "    deployment: shop-api\n", "", 1), "fault.kube.deployment is required"},
		{"name with spaces", "name: disk full\n" + validKube, `name "disk full" must be lowercase letters, digits and dashes`},
		{"name with slash", "name: a/b\n" + validKube, `name "a/b" must be`},
		{"no labels", strings.Replace(validKube, "  labels: {job: shop-api}\n", "  {}\n", 1), "target.labels must name at least one label"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := validateErrs(t, c.body)
			if !strings.Contains(got, c.want) {
				t.Errorf("errors %q do not contain %q", got, c.want)
			}
		})
	}
}

func TestLoadDirSortsAndSkipsNonYAML(t *testing.T) {
	p := fixture(t, "b.yaml", validKube)
	dir := filepath.Dir(p)
	if err := os.WriteFile(filepath.Join(dir, "a.yml"), []byte(validKube), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ds, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 2 || ds[0].Name != "a" || ds[1].Name != "b" {
		t.Fatalf("got %d drills: %v", len(ds), ds)
	}
}

func TestLoadFixesList(t *testing.T) {
	d, err := Load(fixture(t, "x.yaml", validKube+"fixes: [scale-up]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Fixes) != 1 || d.Fixes[0] != "scale-up" {
		t.Fatalf("fixes = %v", d.Fixes)
	}
}
