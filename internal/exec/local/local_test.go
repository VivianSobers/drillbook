package local

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/VivianSobers/drillbook/internal/runbook"
)

func blk(script string, timeout time.Duration) runbook.Block {
	return runbook.Block{Name: "b", Kind: runbook.Check, Target: "runner", Lang: "sh", Script: script, Timeout: timeout}
}

func TestRunBlockPassesEnvAndKubeconfig(t *testing.T) {
	var out bytes.Buffer
	r := &Runner{Kubeconfig: "/kc"}
	err := r.RunBlock(context.Background(), blk("echo node=$NODE kc=$KUBECONFIG\n", time.Minute), map[string]string{"NODE": "w"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "node=w kc=/kc") {
		t.Errorf("output = %q", out.String())
	}
}

func TestRunBlockFailsOnFirstFailingCommand(t *testing.T) {
	var out bytes.Buffer
	err := (&Runner{}).RunBlock(context.Background(), blk("false\necho should-not-run\n", time.Minute), nil, &out)
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(out.String(), "should-not-run") {
		t.Error("strict mode must stop at the first failure")
	}
}

func TestRunBlockUnsetVariableFails(t *testing.T) {
	err := (&Runner{}).RunBlock(context.Background(), blk("echo $DRILLBOOK_SURELY_UNSET\n", time.Minute), nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("unset variable must fail under set -u")
	}
}

func TestRunBlockTimeout(t *testing.T) {
	start := time.Now()
	err := (&Runner{}).RunBlock(context.Background(), blk("sleep 30\n", 200*time.Millisecond), nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "timed out after 200ms") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("timeout not enforced, took %v", time.Since(start))
	}
}
