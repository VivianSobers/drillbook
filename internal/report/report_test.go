package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const lines = `{"drill":"b","id":"b-1","alert":"B","verdict":"not-resolved","findings":["still firing"],"fired_after":"1m45.059s","resolved_after":"0s","receivers":["shop-team"],"control_ran":false,"control_cleared":false,"log":"x"}
{"drill":"a","id":"a-1","alert":"A","verdict":"aborted","findings":["aborted: interrupted"],"fired_after":"0s","resolved_after":"0s","control_ran":false,"control_cleared":false,"log":"x"}
{"drill":"a","id":"a-2","alert":"A","verdict":"pass","fired_after":"16m1.2s","resolved_after":"5m30.6s","receivers":["platform-oncall"],"control_ran":true,"control_cleared":false,"control_cached":true,"log":"x"}
`

func write(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "results.jsonl")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadKeepsLatestPerDrillSortedByName(t *testing.T) {
	rs, err := Load(write(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].Drill != "a" || rs[0].ID != "a-2" || rs[1].Drill != "b" {
		t.Fatalf("got %+v", rs)
	}
}

func TestMarkdown(t *testing.T) {
	rs, err := Load(write(t))
	if err != nil {
		t.Fatal(err)
	}
	got := Markdown(rs)
	for _, want := range []string{
		"| Drill | Alert | Verdict | Fired after | Resolved after | Receivers | Control |",
		"| a | A | pass | 16m1s | 5m31s | platform-oncall | held (cached) |",
		"| b | B | not-resolved | 1m45s | - | shop-team | - |",
		"- b: still firing",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "interrupted") {
		t.Error("only the latest run of each drill belongs in the report")
	}
}

func TestLoadBadLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.jsonl")
	if err := os.WriteFile(p, []byte("{not json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("err = %v", err)
	}
}
