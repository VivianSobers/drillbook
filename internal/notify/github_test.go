package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/VivianSobers/drillbook/internal/engine"
)

// fakeGitHub keeps issues in memory and records the calls made to it.
type fakeGitHub struct {
	mu     sync.Mutex
	issues []map[string]any
	calls  []string
}

func (f *fakeGitHub) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/o/r/issues":
			if r.URL.Query().Get("labels") != "drillbook" || r.URL.Query().Get("state") != "open" {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			var open []map[string]any
			for _, is := range f.issues {
				if is["state"] == "open" {
					open = append(open, is)
				}
			}
			_ = json.NewEncoder(w).Encode(open)
		case r.Method == "POST" && r.URL.Path == "/repos/o/r/issues":
			var in map[string]any
			_ = json.Unmarshal(body, &in)
			in["number"] = float64(len(f.issues) + 1)
			in["state"] = "open"
			f.issues = append(f.issues, in)
			_ = json.NewEncoder(w).Encode(in)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/comments"):
			w.WriteHeader(201)
		case r.Method == "PATCH":
			var in map[string]any
			_ = json.Unmarshal(body, &in)
			for _, is := range f.issues {
				if "/repos/o/r/issues/"+jsonNum(is["number"]) == r.URL.Path {
					is["state"] = in["state"]
				}
			}
			w.WriteHeader(200)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
}

func jsonNum(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func result(drill string, v engine.Verdict, findings ...string) engine.Result {
	return engine.Result{Drill: drill, ID: drill + "-1", Alert: "KubeNodeNotReady", Verdict: v, Findings: findings, LogPath: "/x.log"}
}

func setup(t *testing.T) (*GitHub, *fakeGitHub) {
	t.Helper()
	f := &fakeGitHub{}
	s := httptest.NewServer(f.handler(t))
	t.Cleanup(s.Close)
	return &GitHub{BaseURL: s.URL, Repo: "o/r", Token: "tok"}, f
}

func TestFailureOpensOneIssueThenComments(t *testing.T) {
	g, f := setup(t)
	ctx := context.Background()
	if err := g.Sync(ctx, result("kubelet-stopped", engine.NotResolved, "still firing")); err != nil {
		t.Fatal(err)
	}
	if err := g.Sync(ctx, result("kubelet-stopped", engine.StepFailed, "restart failed")); err != nil {
		t.Fatal(err)
	}
	if len(f.issues) != 1 {
		t.Fatalf("want one issue per failing drill, got %d", len(f.issues))
	}
	is := f.issues[0]
	if is["title"] != "drillbook: kubelet-stopped did not pass" {
		t.Errorf("title = %v", is["title"])
	}
	body, _ := is["body"].(string)
	for _, want := range []string{"KubeNodeNotReady", "not-resolved", "still firing", "kubelet-stopped-1"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(strings.Join(f.calls, ","), "POST /repos/o/r/issues/1/comments") {
		t.Errorf("second failure must comment on the open issue, calls %v", f.calls)
	}
}

func TestPassClosesTheOpenIssue(t *testing.T) {
	g, f := setup(t)
	ctx := context.Background()
	if err := g.Sync(ctx, result("kubelet-stopped", engine.NotResolved)); err != nil {
		t.Fatal(err)
	}
	if err := g.Sync(ctx, result("kubelet-stopped", engine.Pass)); err != nil {
		t.Fatal(err)
	}
	if f.issues[0]["state"] != "closed" {
		t.Fatalf("state = %v", f.issues[0]["state"])
	}
}

func TestPassWithNoIssueDoesNothing(t *testing.T) {
	g, f := setup(t)
	if err := g.Sync(context.Background(), result("a", engine.Pass)); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "GET") {
			t.Errorf("unexpected write %s", c)
		}
	}
}

func TestOtherDrillsIssueIsLeftAlone(t *testing.T) {
	g, f := setup(t)
	ctx := context.Background()
	if err := g.Sync(ctx, result("kubelet-stopped-slow", engine.NotResolved)); err != nil {
		t.Fatal(err)
	}
	if err := g.Sync(ctx, result("kubelet-stopped", engine.Pass)); err != nil {
		t.Fatal(err)
	}
	if f.issues[0]["state"] != "open" {
		t.Fatal("a pass of one drill must not close another drill's issue")
	}
}

func TestBadTokenIsAnError(t *testing.T) {
	g, _ := setup(t)
	g.Token = "wrong"
	if err := g.Sync(context.Background(), result("a", engine.NotResolved)); err == nil {
		t.Fatal("want error")
	}
}
