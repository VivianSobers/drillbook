package wire

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/VivianSobers/drillbook/internal/config"
	"github.com/VivianSobers/drillbook/internal/drill"
	"github.com/VivianSobers/drillbook/internal/runbook"
)

type rec struct{ calls []string }

func (r *rec) add(s string) { r.calls = append(r.calls, s) }

type fakeAnsible struct{ r *rec }

func (f fakeAnsible) Apply(ctx context.Context, a drill.AnsibleFault, host, id string, revertAfter time.Duration, log io.Writer) error {
	f.r.add("ansible.apply " + a.Role + " " + host + " " + id + " " + revertAfter.String())
	return nil
}
func (f fakeAnsible) Revert(ctx context.Context, a drill.AnsibleFault, host, id string, log io.Writer) error {
	f.r.add("ansible.revert " + a.Role + " " + host + " " + id)
	return nil
}
func (f fakeAnsible) RunBlock(ctx context.Context, b runbook.Block, host string, env map[string]string, log io.Writer) error {
	f.r.add("ansible.block " + b.Name + " " + host + " NODE=" + env["NODE"])
	return nil
}

type fakeKube struct{ r *rec }

func (f fakeKube) Apply(ctx context.Context, k drill.KubeFault, id string) error {
	f.r.add("kube.apply " + k.Deployment + " " + id)
	return nil
}
func (f fakeKube) Revert(ctx context.Context, k drill.KubeFault, id string) error {
	f.r.add("kube.revert " + k.Deployment + " " + id)
	return nil
}

type fakeLocal struct{ r *rec }

func (f fakeLocal) RunBlock(ctx context.Context, b runbook.Block, env map[string]string, log io.Writer) error {
	f.r.add("local.block " + b.Name + " NODE=" + env["NODE"])
	return nil
}

func ansibleDrill() *drill.Drill {
	return &drill.Drill{
		Target: drill.Target{Host: "worker", Env: map[string]string{"NODE": "worker"}},
		Fault:  drill.Fault{Ansible: &drill.AnsibleFault{Role: "drillbook.faults.stop_service"}, RevertAfter: config.Duration{Duration: time.Hour}},
	}
}

func allow() config.Allowlist { return config.Allowlist{Hosts: []string{"worker", "bastion"}} }

func TestFaultsDispatchByKind(t *testing.T) {
	r := &rec{}
	f := &Faults{Ansible: fakeAnsible{r}, Kube: fakeKube{r}}
	ctx := context.Background()
	if err := f.Apply(ctx, ansibleDrill(), "id1", io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := f.Revert(ctx, ansibleDrill(), "id1", io.Discard); err != nil {
		t.Fatal(err)
	}
	kd := &drill.Drill{Fault: drill.Fault{Kube: &drill.KubeFault{Deployment: "shop-api"}}}
	if err := f.Apply(ctx, kd, "id2", io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := f.Revert(ctx, kd, "id2", io.Discard); err != nil {
		t.Fatal(err)
	}
	want := "ansible.apply drillbook.faults.stop_service worker id1 1h0m0s|ansible.revert drillbook.faults.stop_service worker id1|kube.apply shop-api id2|kube.revert shop-api id2"
	if got := strings.Join(r.calls, "|"); got != want {
		t.Fatalf("calls\n got %s\nwant %s", got, want)
	}
}

func TestBlocksDispatchByTarget(t *testing.T) {
	r := &rec{}
	b := &Blocks{Local: fakeLocal{r}, Ansible: fakeAnsible{r}, Allow: allow()}
	ctx := context.Background()
	d := ansibleDrill()
	for _, blk := range []runbook.Block{
		{Name: "a", Target: "runner"},
		{Name: "b", Target: "node"},
		{Name: "c", Target: "host:bastion"},
	} {
		if err := b.RunBlock(ctx, blk, d, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	want := "local.block a NODE=worker|ansible.block b worker NODE=worker|ansible.block c bastion NODE=worker"
	if got := strings.Join(r.calls, "|"); got != want {
		t.Fatalf("calls\n got %s\nwant %s", got, want)
	}
}

func TestNodeBlockWithoutHostFails(t *testing.T) {
	b := &Blocks{Local: fakeLocal{&rec{}}, Ansible: fakeAnsible{&rec{}}, Allow: allow()}
	d := &drill.Drill{}
	if err := b.RunBlock(context.Background(), runbook.Block{Name: "b", Target: "node"}, d, io.Discard); err == nil {
		t.Fatal("node block on a drill without target.host must fail")
	}
}

func TestHostBlockOutsideAllowlistFails(t *testing.T) {
	r := &rec{}
	b := &Blocks{Local: fakeLocal{r}, Ansible: fakeAnsible{r}, Allow: allow()}
	err := b.RunBlock(context.Background(), runbook.Block{Name: "x", Target: "host:prod-db"}, ansibleDrill(), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "prod-db") {
		t.Fatalf("err = %v", err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("nothing may run, got %v", r.calls)
	}
}

func TestKubeFaultsWithoutKubeconfigOutsideAClusterExplainsWhy(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	l := &lazyKube{}
	err := l.Apply(context.Background(), drill.KubeFault{}, "id")
	if err == nil || !strings.Contains(err.Error(), "no kubeconfig") || !strings.Contains(err.Error(), "in-cluster") {
		t.Fatalf("err = %v", err)
	}
}
