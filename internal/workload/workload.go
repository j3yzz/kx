// Package workload finds the Deployment, StatefulSet or DaemonSet an app name
// refers to and changes it (restart, scale). Unlike the resolver, it matches
// the workload name exactly and never guesses through labels: these actions
// change the cluster, so there must be no doubt about the target.
package workload

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/j3yzz/kx/internal/kube"
	"github.com/j3yzz/kx/internal/resolver"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// Workload is a found workload. Resource is the plural for RBAC checks.
type Workload struct {
	Kind, Resource, Name string
	Replicas             int32 // -1 for DaemonSets
}

func (w Workload) String() string { return w.Kind + "/" + w.Name }

// Find looks for a Deployment, then StatefulSet, then DaemonSet named app.
func Find(ctx context.Context, cs kubernetes.Interface, ns, app string) (*Workload, error) {
	if d, err := cs.AppsV1().Deployments(ns).Get(ctx, app, metav1.GetOptions{}); err == nil {
		return &Workload{"Deployment", "deployments", app, replicas(d.Spec.Replicas)}, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, kube.WrapError(err)
	}
	if s, err := cs.AppsV1().StatefulSets(ns).Get(ctx, app, metav1.GetOptions{}); err == nil {
		return &Workload{"StatefulSet", "statefulsets", app, replicas(s.Spec.Replicas)}, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, kube.WrapError(err)
	}
	if _, err := cs.AppsV1().DaemonSets(ns).Get(ctx, app, metav1.GetOptions{}); err == nil {
		return &Workload{"DaemonSet", "daemonsets", app, -1}, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, kube.WrapError(err)
	}
	return nil, fmt.Errorf("%w: no Deployment, StatefulSet or DaemonSet named %q in namespace %s (restart and scale need the exact workload name)",
		resolver.ErrNotFound, app, ns)
}

func replicas(r *int32) int32 {
	if r == nil {
		return 1
	}
	return *r
}

// Restart triggers a rolling restart by stamping the pod template, the same
// way `kubectl rollout restart` does.
func Restart(ctx context.Context, cs kubernetes.Interface, ns string, w *Workload, now time.Time) error {
	return patch(ctx, cs, ns, w, map[string]any{"spec": map[string]any{"template": map[string]any{
		"metadata": map[string]any{"annotations": map[string]string{
			"kubectl.kubernetes.io/restartedAt": now.UTC().Format(time.RFC3339),
		}}}}})
}

// Scale sets the replica count. DaemonSets cannot be scaled.
func Scale(ctx context.Context, cs kubernetes.Interface, ns string, w *Workload, n int32) error {
	if w.Kind == "DaemonSet" {
		return fmt.Errorf("%s cannot be scaled; it runs one pod per node", w)
	}
	if n < 0 {
		return fmt.Errorf("replicas must be 0 or more")
	}
	return patch(ctx, cs, ns, w, map[string]any{"spec": map[string]any{"replicas": n}})
}

func patch(ctx context.Context, cs kubernetes.Interface, ns string, w *Workload, body map[string]any) error {
	b, _ := json.Marshal(body)
	var err error
	switch w.Kind {
	case "Deployment":
		_, err = cs.AppsV1().Deployments(ns).Patch(ctx, w.Name, types.MergePatchType, b, metav1.PatchOptions{})
	case "StatefulSet":
		_, err = cs.AppsV1().StatefulSets(ns).Patch(ctx, w.Name, types.MergePatchType, b, metav1.PatchOptions{})
	case "DaemonSet":
		_, err = cs.AppsV1().DaemonSets(ns).Patch(ctx, w.Name, types.MergePatchType, b, metav1.PatchOptions{})
	}
	return kube.WrapError(err)
}
