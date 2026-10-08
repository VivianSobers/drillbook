package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VivianSobers/drillbook/internal/config"
	"github.com/VivianSobers/drillbook/internal/drill"
)

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
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

const goodRunbook = "# X\n\n```sh {\"name\":\"fix\",\"drill\":\"fix\"}\ntrue\n```\n"

const goodDrill = `alert: ShopApiDown
runbook: ../runbooks/ShopApiDown.md
target: {labels: {job: shop-api}}
fault: {kube: {namespace: shop, deployment: shop-api, scale: 0}}
fire_within: 3m
resolve_within: 3m
`

func cfg() *config.Config {
	return &config.Config{Allow: config.Allowlist{Namespaces: []string{"shop"}}}
}

func load(t *testing.T, dir string) []*drill.Drill {
	t.Helper()
	ds, err := drill.LoadDir(filepath.Join(dir, "drills"))
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func joined(ps []Problem) string {
	var s []string
	for _, p := range ps {
		s = append(s, p.String())
	}
	return strings.Join(s, "\n")
}

func TestCleanRepoHasNoProblems(t *testing.T) {
	dir := repo(t, map[string]string{"drills/a.yaml": goodDrill, "runbooks/ShopApiDown.md": goodRunbook})
	if ps := Drills(cfg(), load(t, dir)); len(ps) != 0 {
		t.Fatalf("problems: %s", joined(ps))
	}
}

func TestRunbookWithoutFixBlock(t *testing.T) {
	dir := repo(t, map[string]string{"drills/a.yaml": goodDrill, "runbooks/ShopApiDown.md": "# no steps\n"})
	got := joined(Drills(cfg(), load(t, dir)))
	if !strings.Contains(got, "has no fix blocks") || !strings.Contains(got, "ShopApiDown.md") {
		t.Fatalf("got %q", got)
	}
}

func TestRunbookParseErrorReported(t *testing.T) {
	dir := repo(t, map[string]string{"drills/a.yaml": goodDrill, "runbooks/ShopApiDown.md": "```sh {\"drill\":\"maybe\"}\nx\n```\n"})
	got := joined(Drills(cfg(), load(t, dir)))
	if !strings.Contains(got, `line 1: drill "maybe"`) {
		t.Fatalf("got %q", got)
	}
}

func TestValidationErrorsReported(t *testing.T) {
	dir := repo(t, map[string]string{"drills/a.yaml": strings.Replace(goodDrill, "namespace: shop", "namespace: prod", 1), "runbooks/ShopApiDown.md": goodRunbook})
	if got := joined(Drills(cfg(), load(t, dir))); !strings.Contains(got, `namespace "prod" is not in allow.namespaces`) {
		t.Fatalf("got %q", got)
	}
}

func TestRulesWithoutRunbookURL(t *testing.T) {
	dir := repo(t, map[string]string{
		"rules/plain.yaml": `groups:
- name: g
  rules:
  - alert: HasURL
    expr: up == 0
    annotations: {runbook_url: https://x}
  - alert: NoURL
    expr: up == 0
  - record: job:up:sum
    expr: sum(up)
`,
		"rules/cr.yaml": `apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata: {name: shop}
spec:
  groups:
  - name: shop
    rules:
    - alert: CRNoURL
      expr: up == 0
      annotations: {summary: x}
`,
	})
	ps, err := Rules([]string{filepath.Join(dir, "rules", "*.yaml")})
	if err != nil {
		t.Fatal(err)
	}
	got := joined(ps)
	if !strings.Contains(got, "NoURL has no runbook_url") || !strings.Contains(got, "CRNoURL has no runbook_url") || strings.Contains(got, "HasURL") || strings.Contains(got, "job:up:sum") {
		t.Fatalf("got %q", got)
	}
}

func TestRulesGlobMatchingNothingIsAnError(t *testing.T) {
	if _, err := Rules([]string{filepath.Join(t.TempDir(), "*.yaml")}); err == nil {
		t.Fatal("a rules glob that matches nothing is almost always a typo")
	}
}

func TestUnknownFixNameReported(t *testing.T) {
	dir := repo(t, map[string]string{"drills/a.yaml": goodDrill + "fixes: [scale-up]\n", "runbooks/ShopApiDown.md": goodRunbook})
	if got := joined(Drills(cfg(), load(t, dir))); !strings.Contains(got, `no fix block named "scale-up"`) {
		t.Fatalf("got %q", got)
	}
}
