package affected

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/VivianSobers/drillbook/internal/drill"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, p, body string) {
	t.Helper()
	full := filepath.Join(dir, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func drillYAML(alert, rb, fire string) string {
	return "alert: " + alert + "\nrunbook: ../runbooks/" + rb + "\ntarget: {labels: {a: b}}\nfault: {kube: {namespace: shop, deployment: x, scale: 0}}\nfire_within: " + fire + "\nresolve_within: 3m\n"
}

// setupRepo creates three drills and a rules file, commits them, and returns the repo dir.
func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "drills/fast.yaml", drillYAML("ShopApiDown", "ShopApiDown.md", "3m"))
	write(t, dir, "drills/slow.yaml", drillYAML("KubeNodeNotReady", "KubeNodeNotReady.md", "20m"))
	write(t, dir, "drills/other.yaml", drillYAML("KubePodCrashLooping", "KubePodCrashLooping.md", "20m"))
	for _, rb := range []string{"ShopApiDown.md", "KubeNodeNotReady.md", "KubePodCrashLooping.md"} {
		write(t, dir, "runbooks/"+rb, "# "+rb+"\n")
	}
	write(t, dir, "rules/shop.yaml", "groups:\n- name: shop\n  rules:\n  - alert: ShopApiDown\n    expr: absent(up)\n")
	write(t, dir, "README.md", "x\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func drills(t *testing.T, dir string) []*drill.Drill {
	t.Helper()
	ds, err := drill.LoadDir(filepath.Join(dir, "drills"))
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func names(ds []*drill.Drill) []string {
	out := []string{}
	for _, d := range ds {
		out = append(out, d.Name)
	}
	return out
}

func commitChange(t *testing.T, dir, p, body string) {
	t.Helper()
	write(t, dir, p, body)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "change")
}

func TestRunbookChangeSelectsItsDrill(t *testing.T) {
	dir := setupRepo(t)
	commitChange(t, dir, "runbooks/KubeNodeNotReady.md", "# changed\n")
	got, err := Find(dir, "HEAD~1..HEAD", drills(t, dir), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names(got), []string{"slow"}) {
		t.Fatalf("got %v", names(got))
	}
}

func TestDrillFileChangeSelectsIt(t *testing.T) {
	dir := setupRepo(t)
	commitChange(t, dir, "drills/other.yaml", drillYAML("KubePodCrashLooping", "KubePodCrashLooping.md", "25m"))
	got, _ := Find(dir, "HEAD~1..HEAD", drills(t, dir), 0)
	if !reflect.DeepEqual(names(got), []string{"other"}) {
		t.Fatalf("got %v", names(got))
	}
}

func TestRulesChangeSelectsDrillsForItsAlerts(t *testing.T) {
	dir := setupRepo(t)
	commitChange(t, dir, "rules/shop.yaml", "groups:\n- name: shop\n  rules:\n  - alert: ShopApiDown\n    expr: absent(up{job=\"shop-api\"})\n")
	got, _ := Find(dir, "HEAD~1..HEAD", drills(t, dir), 0)
	if !reflect.DeepEqual(names(got), []string{"fast"}) {
		t.Fatalf("got %v", names(got))
	}
}

func TestUnrelatedChangeSelectsNothing(t *testing.T) {
	dir := setupRepo(t)
	commitChange(t, dir, "README.md", "y\n")
	got, _ := Find(dir, "HEAD~1..HEAD", drills(t, dir), 0)
	if len(got) != 0 {
		t.Fatalf("got %v", names(got))
	}
}

func TestMaxFireWithinFilters(t *testing.T) {
	dir := setupRepo(t)
	commitChange(t, dir, "runbooks/KubeNodeNotReady.md", "# changed\n")
	write(t, dir, "runbooks/ShopApiDown.md", "# changed too\n")
	git(t, dir, "commit", "-qam", "both")
	got, _ := Find(dir, "HEAD~2..HEAD", drills(t, dir), 20*time.Minute-time.Second)
	if !reflect.DeepEqual(names(got), []string{"fast"}) {
		t.Fatalf("got %v", names(got))
	}
}

func TestDeletedRulesFileIsSkipped(t *testing.T) {
	dir := setupRepo(t)
	git(t, dir, "rm", "-q", "rules/shop.yaml")
	git(t, dir, "commit", "-q", "-m", "rm")
	if _, err := Find(dir, "HEAD~1..HEAD", drills(t, dir), 0); err != nil {
		t.Fatalf("a deleted file must not break affected: %v", err)
	}
}

func TestBadRangeIsError(t *testing.T) {
	dir := setupRepo(t)
	if _, err := Find(dir, "nope..HEAD", drills(t, dir), 0); err == nil {
		t.Fatal("want error")
	}
}
