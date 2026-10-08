package state

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActiveRoundTrip(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	a := Active{ID: "d-1", DrillFile: "/r/drills/d.yaml", SilenceID: "s1", Started: time.Unix(100, 0).UTC()}
	if err := s.MarkActive(a); err != nil {
		t.Fatal(err)
	}
	got, err := s.Active()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != a {
		t.Fatalf("active = %+v", got)
	}
	if err := s.ClearActive("d-1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Active(); len(got) != 0 {
		t.Fatalf("active after clear = %+v", got)
	}
	if err := s.ClearActive("d-1"); err != nil {
		t.Fatalf("clearing twice must be fine: %v", err)
	}
}

func TestActiveOnEmptyDir(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "missing")}
	got, err := s.Active()
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v err %v", got, err)
	}
}

func TestAppendResultWritesJSONLines(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	for _, v := range []string{"pass", "not-resolved"} {
		if err := s.AppendResult(map[string]string{"verdict": v}); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(filepath.Join(s.Dir, "results.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]string
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		got = append(got, m["verdict"])
	}
	if len(got) != 2 || got[0] != "pass" || got[1] != "not-resolved" {
		t.Fatalf("lines = %v", got)
	}
}

func TestControlCache(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if _, ok := s.Control("h1"); ok {
		t.Fatal("empty cache must miss")
	}
	if err := s.SaveControl("h1", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveControl("h2", false); err != nil {
		t.Fatal(err)
	}
	if c, ok := s.Control("h1"); !ok || !c {
		t.Fatalf("h1 = %v %v", c, ok)
	}
	if c, ok := s.Control("h2"); !ok || c {
		t.Fatalf("h2 = %v %v", c, ok)
	}
}

func TestLogFileCreatesLogsDir(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	w, p, err := s.LogFile("d-1")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("hello\n"))
	w.Close()
	if filepath.Base(filepath.Dir(p)) != "logs" {
		t.Errorf("path = %s", p)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "hello\n" {
		t.Errorf("content = %q", b)
	}
}
