// Package runbook extracts the steps drillbook runs from a markdown runbook.
//
// Only fenced code blocks whose info string carries Runme-style JSON
// attributes with a "drill" key are steps:
//
//	```sh {"name":"restart","drill":"fix","target":"node"}
//	systemctl restart kubelet
//	```
//
// Everything else in the document is prose for people and is ignored.
package runbook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

type Kind string

const (
	Check Kind = "check"
	Fix   Kind = "fix"
)

const DefaultTimeout = 5 * time.Minute

type Block struct {
	Name string
	Kind Kind
	// Target is "runner", "node" or "host:<inventory-name>".
	Target  string
	Lang    string
	Script  string
	Line    int // line of the opening fence, 1-based
	Timeout time.Duration
}

type Runbook struct {
	Path   string
	Blocks []Block
}

func (r *Runbook) Checks() []Block { return r.filter(Check) }
func (r *Runbook) Fixes() []Block  { return r.filter(Fix) }

func (r *Runbook) filter(k Kind) []Block {
	var out []Block
	for _, b := range r.Blocks {
		if b.Kind == k {
			out = append(out, b)
		}
	}
	return out
}

var shells = map[string]bool{"sh": true, "bash": true, "shell": true}

// Parse reads a runbook and returns its tagged blocks in document order.
func Parse(path string) (*Runbook, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rb := &Runbook{Path: path}
	doc := goldmark.New().Parser().Parse(text.NewReader(src))
	var walkErr error
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		fb, ok := n.(*ast.FencedCodeBlock)
		if !entering || !ok || fb.Info == nil {
			return ast.WalkContinue, nil
		}
		b, tagged, err := block(fb, src)
		if err != nil {
			walkErr = fmt.Errorf("%s: %w", path, err)
			return ast.WalkStop, nil
		}
		if tagged {
			rb.Blocks = append(rb.Blocks, b)
		}
		return ast.WalkContinue, nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return rb, nil
}

type attrs struct {
	Name    string `json:"name"`
	Drill   string `json:"drill"`
	Target  string `json:"target"`
	Timeout string `json:"timeout"`
}

func block(fb *ast.FencedCodeBlock, src []byte) (Block, bool, error) {
	info := string(fb.Info.Segment.Value(src))
	line := bytes.Count(src[:fb.Info.Segment.Start], []byte("\n")) + 1
	lang, rest, _ := strings.Cut(strings.TrimSpace(info), " ")
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, "{") || !strings.Contains(rest, `"drill"`) {
		return Block{}, false, nil
	}
	fail := func(format string, a ...any) (Block, bool, error) {
		return Block{}, false, fmt.Errorf("line %d: "+format, append([]any{line}, a...)...)
	}
	var a attrs
	if err := json.Unmarshal([]byte(rest), &a); err != nil {
		return fail("attributes %s: %v", rest, err)
	}
	b := Block{Name: a.Name, Kind: Kind(a.Drill), Target: a.Target, Lang: lang, Line: line, Timeout: DefaultTimeout}
	if b.Kind != Check && b.Kind != Fix {
		return fail("drill %q must be check or fix", a.Drill)
	}
	if !shells[lang] {
		return fail("language %q is not supported; use sh or bash", lang)
	}
	if b.Target == "" {
		b.Target = "runner"
	}
	if b.Target != "runner" && b.Target != "node" && (!strings.HasPrefix(b.Target, "host:") || b.Target == "host:") {
		return fail("target %q must be runner, node or host:<name>", b.Target)
	}
	if a.Timeout != "" {
		d, err := time.ParseDuration(a.Timeout)
		if err != nil || d <= 0 {
			return fail("timeout %q is not a positive duration", a.Timeout)
		}
		b.Timeout = d
	}
	var sb strings.Builder
	for i := 0; i < fb.Lines().Len(); i++ {
		seg := fb.Lines().At(i)
		sb.Write(seg.Value(src))
	}
	b.Script = sb.String()
	if strings.TrimSpace(b.Script) == "" {
		return fail("block is empty")
	}
	if b.Name == "" {
		b.Name = fmt.Sprintf("line-%d", line)
	}
	return b, true, nil
}
