package runbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rb.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const sample = "# KubeNodeNotReady\n" +
	"\n" +
	"Plain block, ignored:\n" +
	"\n" +
	"```sh\n" +
	"echo not a drill step\n" +
	"```\n" +
	"\n" +
	"## Diagnose\n" +
	"\n" +
	"```sh {\"name\":\"node-status\",\"drill\":\"check\"}\n" +
	"kubectl get node \"$NODE\" -o wide\n" +
	"```\n" +
	"\n" +
	"```sh {\"name\":\"kubelet-log\",\"drill\":\"check\",\"target\":\"node\",\"timeout\":\"30s\"}\n" +
	"journalctl -u kubelet -n 50 --no-pager\n" +
	"```\n" +
	"\n" +
	"## Fix\n" +
	"\n" +
	"```bash {\"name\":\"restart\",\"drill\":\"fix\",\"target\":\"node\",\"interactive\":false}\n" +
	"systemctl restart kubelet\n" +
	"systemctl is-active kubelet\n" +
	"```\n" +
	"\n" +
	"```sh {\"name\":\"other-host\",\"drill\":\"fix\",\"target\":\"host:bastion\"}\n" +
	"true\n" +
	"```\n"

func TestParseKeepsTaggedBlocksInOrder(t *testing.T) {
	rb, err := Parse(write(t, sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(rb.Blocks) != 4 {
		t.Fatalf("want 4 tagged blocks, got %d: %+v", len(rb.Blocks), rb.Blocks)
	}
	b := rb.Blocks[0]
	if b.Name != "node-status" || b.Kind != Check || b.Target != "runner" || b.Lang != "sh" {
		t.Errorf("block 0 = %+v", b)
	}
	if b.Script != "kubectl get node \"$NODE\" -o wide\n" {
		t.Errorf("script = %q", b.Script)
	}
	if b.Line != 11 {
		t.Errorf("line = %d, want 11 (the fence line)", b.Line)
	}
	if b.Timeout != 5*time.Minute {
		t.Errorf("default timeout = %v", b.Timeout)
	}
	if rb.Blocks[1].Timeout != 30*time.Second || rb.Blocks[1].Target != "node" {
		t.Errorf("block 1 = %+v", rb.Blocks[1])
	}
	if rb.Blocks[2].Script != "systemctl restart kubelet\nsystemctl is-active kubelet\n" {
		t.Errorf("multi-line script = %q", rb.Blocks[2].Script)
	}
	if rb.Blocks[3].Target != "host:bastion" {
		t.Errorf("host target = %q", rb.Blocks[3].Target)
	}
	if got := len(rb.Checks()); got != 2 {
		t.Errorf("Checks() = %d", got)
	}
	fixes := rb.Fixes()
	if len(fixes) != 2 || fixes[0].Name != "restart" {
		t.Errorf("Fixes() = %+v", fixes)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"bad kind", "```sh {\"drill\":\"repair\"}\nx\n```\n", `line 1: drill "repair" must be check or fix`},
		{"bad json", "text\n\n```sh {\"drill\":\"fix\"\nx\n```\n", "line 3: attributes"},
		{"bad target", "```sh {\"drill\":\"fix\",\"target\":\"laptop\"}\nx\n```\n", `line 1: target "laptop" must be runner, node or host:<name>`},
		{"empty host", "```sh {\"drill\":\"fix\",\"target\":\"host:\"}\nx\n```\n", `target "host:"`},
		{"bad timeout", "```sh {\"drill\":\"fix\",\"timeout\":\"soon\"}\nx\n```\n", "line 1: timeout"},
		{"unsupported lang", "```python {\"drill\":\"fix\"}\nx\n```\n", `line 1: language "python" is not supported`},
		{"empty script", "```sh {\"drill\":\"fix\"}\n```\n", "line 1: block is empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(write(t, c.body))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestParseIgnoresAttributesWithoutDrillKey(t *testing.T) {
	rb, err := Parse(write(t, "```sh {\"name\":\"runme-only\"}\necho hi\n```\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rb.Blocks) != 0 {
		t.Fatalf("Runme-only block must be ignored, got %+v", rb.Blocks)
	}
}

func TestParseIgnoresIndentedAndTildeFencesConsistently(t *testing.T) {
	body := "~~~sh {\"drill\":\"fix\"}\necho tilde\n~~~\n"
	rb, err := Parse(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if len(rb.Blocks) != 1 || rb.Blocks[0].Script != "echo tilde\n" {
		t.Fatalf("tilde fence not parsed: %+v", rb.Blocks)
	}
}
