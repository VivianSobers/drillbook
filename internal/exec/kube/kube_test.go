package kube

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/VivianSobers/drillbook/internal/drill"
)

func deployment() *appsv1.Deployment {
	two := int32(2)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "shop-api", Namespace: "shop"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &two,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "shop-api"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name: "app", Image: "example:v1", Args: []string{"--port=8080"},
				}}},
			},
		},
	}
}

func setup(t *testing.T) (*Faults, *fake.Clientset, string) {
	t.Helper()
	cs := fake.NewClientset(deployment())
	dir := t.TempDir()
	return &Faults{Client: cs, SnapshotDir: dir}, cs, dir
}

func get(t *testing.T, cs *fake.Clientset) *appsv1.Deployment {
	t.Helper()
	d, err := cs.AppsV1().Deployments("shop").Get(context.Background(), "shop-api", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func scale(n int32) drill.KubeFault {
	return drill.KubeFault{Namespace: "shop", Deployment: "shop-api", Scale: &n}
}

func TestScaleAndRevert(t *testing.T) {
	f, cs, dir := setup(t)
	ctx := context.Background()
	if err := f.Apply(ctx, scale(0), "d1"); err != nil {
		t.Fatal(err)
	}
	if r := *get(t, cs).Spec.Replicas; r != 0 {
		t.Fatalf("replicas after apply = %d", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "d1.kube.json")); err != nil {
		t.Fatalf("snapshot not saved: %v", err)
	}
	if err := f.Revert(ctx, scale(0), "d1"); err != nil {
		t.Fatal(err)
	}
	if r := *get(t, cs).Spec.Replicas; r != 2 {
		t.Fatalf("replicas after revert = %d", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "d1.kube.json")); !os.IsNotExist(err) {
		t.Fatalf("snapshot must be removed after revert, stat err = %v", err)
	}
}

func TestPatchAndRevertRestoresTemplate(t *testing.T) {
	f, cs, _ := setup(t)
	ctx := context.Background()
	p := drill.KubeFault{Namespace: "shop", Deployment: "shop-api", Patch: &drill.Patch{
		Type: "json",
		Body: []any{map[string]any{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--no-such-flag"}},
	}}
	if err := f.Apply(ctx, p, "d2"); err != nil {
		t.Fatal(err)
	}
	if args := get(t, cs).Spec.Template.Spec.Containers[0].Args; len(args) != 2 || args[1] != "--no-such-flag" {
		t.Fatalf("args after patch = %v", args)
	}
	if err := f.Revert(ctx, p, "d2"); err != nil {
		t.Fatal(err)
	}
	if args := get(t, cs).Spec.Template.Spec.Containers[0].Args; len(args) != 1 {
		t.Fatalf("args after revert = %v", args)
	}
}

func TestMergePatch(t *testing.T) {
	f, cs, _ := setup(t)
	p := drill.KubeFault{Namespace: "shop", Deployment: "shop-api", Patch: &drill.Patch{
		Type: "strategic",
		Body: map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"containers": []any{map[string]any{"name": "app", "image": "example:does-not-exist"}}}}}},
	}}
	if err := f.Apply(context.Background(), p, "d3"); err != nil {
		t.Fatal(err)
	}
	if img := get(t, cs).Spec.Template.Spec.Containers[0].Image; img != "example:does-not-exist" {
		t.Fatalf("image = %s", img)
	}
}

func TestSecondApplyKeepsFirstSnapshot(t *testing.T) {
	f, cs, _ := setup(t)
	ctx := context.Background()
	if err := f.Apply(ctx, scale(1), "d4"); err != nil {
		t.Fatal(err)
	}
	if err := f.Apply(ctx, scale(0), "d4"); err != nil {
		t.Fatal(err)
	}
	if err := f.Revert(ctx, scale(0), "d4"); err != nil {
		t.Fatal(err)
	}
	if r := *get(t, cs).Spec.Replicas; r != 2 {
		t.Fatalf("revert must restore the original 2, got %d", r)
	}
}

func TestRevertAfterRunbookAlreadyFixedIt(t *testing.T) {
	f, cs, _ := setup(t)
	ctx := context.Background()
	if err := f.Apply(ctx, scale(0), "d5"); err != nil {
		t.Fatal(err)
	}
	d := get(t, cs)
	two := int32(2)
	d.Spec.Replicas = &two
	if _, err := cs.AppsV1().Deployments("shop").Update(ctx, d, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	before := len(cs.Actions())
	if err := f.Revert(ctx, scale(0), "d5"); err != nil {
		t.Fatal(err)
	}
	for _, a := range cs.Actions()[before:] {
		if a.GetVerb() == "update" {
			t.Errorf("no update expected when already restored, got %v", a)
		}
	}
}

func TestRevertWithoutSnapshotIsNoop(t *testing.T) {
	f, _, _ := setup(t)
	if err := f.Revert(context.Background(), scale(0), "never-applied"); err != nil {
		t.Fatal(err)
	}
}

func TestApplyMissingDeploymentFails(t *testing.T) {
	f, _, _ := setup(t)
	k := scale(0)
	k.Deployment = "nope"
	if err := f.Apply(context.Background(), k, "d6"); err == nil {
		t.Fatal("want error")
	}
}
