// Package kube applies and reverts faults on Kubernetes Deployments: scaling
// them or patching them, as a bad deploy would. Before the first change it
// saves the Deployment's replicas and pod template to a snapshot file, so a
// later run (or `drillbook abort`) can restore it even after a crash.
package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"

	"github.com/VivianSobers/drillbook/internal/drill"
)

type Faults struct {
	Client      kubernetes.Interface
	SnapshotDir string
}

// New builds Faults from a kubeconfig file.
func New(kubeconfig, snapshotDir string) (*Faults, error) {
	rc, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	return &Faults{Client: cs, SnapshotDir: snapshotDir}, nil
}

type snapshot struct {
	Namespace  string                 `json:"namespace"`
	Deployment string                 `json:"deployment"`
	Replicas   *int32                 `json:"replicas"`
	Template   corev1.PodTemplateSpec `json:"template"`
}

func (f *Faults) snapPath(drillID string) string {
	return filepath.Join(f.SnapshotDir, drillID+".kube.json")
}

var patchTypes = map[string]types.PatchType{
	"json":      types.JSONPatchType,
	"merge":     types.MergePatchType,
	"strategic": types.StrategicMergePatchType,
}

func (f *Faults) Apply(ctx context.Context, k drill.KubeFault, drillID string) error {
	deps := f.Client.AppsV1().Deployments(k.Namespace)
	d, err := deps.Get(ctx, k.Deployment, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get deployment %s/%s: %w", k.Namespace, k.Deployment, err)
	}
	if err := f.saveSnapshot(drillID, d); err != nil {
		return err
	}
	switch {
	case k.Scale != nil:
		n := *k.Scale
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			cur, err := deps.Get(ctx, k.Deployment, metav1.GetOptions{})
			if err != nil {
				return err
			}
			cur.Spec.Replicas = &n
			_, err = deps.Update(ctx, cur, metav1.UpdateOptions{})
			return err
		})
	case k.Patch != nil:
		pt, ok := patchTypes[k.Patch.Type]
		if !ok {
			return fmt.Errorf("unknown patch type %q", k.Patch.Type)
		}
		var body []byte
		if body, err = json.Marshal(k.Patch.Body); err != nil {
			return err
		}
		_, err = deps.Patch(ctx, k.Deployment, pt, body, metav1.PatchOptions{})
	default:
		return errors.New("kube fault needs scale or patch")
	}
	if err != nil {
		return fmt.Errorf("apply fault to %s/%s: %w", k.Namespace, k.Deployment, err)
	}
	return nil
}

// saveSnapshot keeps the first snapshot for a drill ID: a retried Apply must
// not record the already-broken state as the one to restore.
func (f *Faults) saveSnapshot(drillID string, d *appsv1.Deployment) error {
	p := f.snapPath(drillID)
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	b, err := json.Marshal(snapshot{Namespace: d.Namespace, Deployment: d.Name, Replicas: d.Spec.Replicas, Template: d.Spec.Template})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(f.SnapshotDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

// Revert restores the snapshot's replicas and pod template if they differ,
// then deletes the snapshot. With no snapshot there is nothing to undo.
func (f *Faults) Revert(ctx context.Context, _ drill.KubeFault, drillID string) error {
	p := f.snapPath(drillID)
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var s snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("snapshot %s: %w", p, err)
	}
	deps := f.Client.AppsV1().Deployments(s.Namespace)
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		d, err := deps.Get(ctx, s.Deployment, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if equality.Semantic.DeepEqual(d.Spec.Replicas, s.Replicas) && equality.Semantic.DeepEqual(d.Spec.Template, s.Template) {
			return nil
		}
		d.Spec.Replicas = s.Replicas
		d.Spec.Template = s.Template
		_, err = deps.Update(ctx, d, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		return fmt.Errorf("restore %s/%s: %w", s.Namespace, s.Deployment, err)
	}
	return os.Remove(p)
}
