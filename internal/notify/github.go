// Package notify keeps one GitHub issue per failing drill: it opens the issue
// on the first failure, comments on later ones, and closes it when the drill
// passes again.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/VivianSobers/drillbook/internal/engine"
)

// Label marks issues drillbook manages.
const Label = "drillbook"

type GitHub struct {
	// BaseURL defaults to https://api.github.com.
	BaseURL string
	// Repo is owner/name.
	Repo  string
	Token string
	HTTP  *http.Client
}

type issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

func title(drill string) string { return "drillbook: " + drill + " did not pass" }

// Sync brings the drill's issue in line with its latest result.
func (g *GitHub) Sync(ctx context.Context, r engine.Result) error {
	open, err := g.find(ctx, r.Drill)
	if err != nil {
		return err
	}
	switch {
	case !r.Verdict.Passed() && open == nil:
		return g.call(ctx, http.MethodPost, "/issues", map[string]any{
			"title": title(r.Drill), "body": body(r), "labels": []string{Label},
		}, nil)
	case !r.Verdict.Passed():
		return g.call(ctx, http.MethodPost, fmt.Sprintf("/issues/%d/comments", open.Number), map[string]any{"body": body(r)}, nil)
	case open != nil:
		if err := g.call(ctx, http.MethodPost, fmt.Sprintf("/issues/%d/comments", open.Number),
			map[string]any{"body": fmt.Sprintf("Drill `%s` passed again in run `%s`. Closing.", r.Drill, r.ID)}, nil); err != nil {
			return err
		}
		return g.call(ctx, http.MethodPatch, fmt.Sprintf("/issues/%d", open.Number), map[string]any{"state": "closed"}, nil)
	}
	return nil
}

func (g *GitHub) find(ctx context.Context, drill string) (*issue, error) {
	var issues []issue
	if err := g.call(ctx, http.MethodGet, "/issues?state=open&labels="+Label+"&per_page=100", nil, &issues); err != nil {
		return nil, err
	}
	for _, is := range issues {
		if is.Title == title(drill) {
			return &is, nil
		}
	}
	return nil, nil
}

func body(r engine.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Drill `%s` for alert `%s` finished with verdict **%s** (run `%s`).\n\n", r.Drill, r.Alert, r.Verdict, r.ID)
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	if r.FiredAfter.Duration > 0 {
		fmt.Fprintf(&b, "\nFired after %v.", r.FiredAfter.Round(time.Second))
	}
	if r.ResolvedAfter.Duration > 0 {
		fmt.Fprintf(&b, " Resolved %v after the runbook started.", r.ResolvedAfter.Round(time.Second))
	}
	fmt.Fprintf(&b, "\n\nFull log on the runner: `%s`\n", r.LogPath)
	return b.String()
}

func (g *GitHub) call(ctx context.Context, method, path string, in, out any) error {
	base := g.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	var rd io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+"/repos/"+g.Repo+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := g.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("github %s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}
