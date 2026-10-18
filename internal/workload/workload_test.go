package workload

import (
	"context"
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/j3yzz/kx/internal/resolver"
)

func TestFindRestartScale(t *testing.T) {
	two := int32(2)
	cs := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "web"}, Spec: appsv1.DeploymentSpec{Replicas: &two}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "db"}},
		&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "agent"}},
	)
	ctx := context.Background()

	w, err := Find(ctx, cs, "n", "web")
	if err != nil || w.String() != "Deployment/web" || w.Replicas != 2 {
		t.Fatalf("find: %+v %v", w, err)
	}
	if err := Scale(ctx, cs, "n", w, 5); err != nil {
		t.Fatal(err)
	}
	d, _ := cs.AppsV1().Deployments("n").Get(ctx, "web", metav1.GetOptions{})
	if *d.Spec.Replicas != 5 {
		t.Fatalf("replicas %d", *d.Spec.Replicas)
	}
	if err := Restart(ctx, cs, "n", w, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	d, _ = cs.AppsV1().Deployments("n").Get(ctx, "web", metav1.GetOptions{})
	if d.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] != "1970-01-01T00:00:00Z" {
		t.Fatalf("annotations %v", d.Spec.Template.Annotations)
	}

	ds, _ := Find(ctx, cs, "n", "agent")
	if Scale(ctx, cs, "n", ds, 1) == nil {
		t.Fatal("scaled a DaemonSet")
	}
	if Restart(ctx, cs, "n", ds, time.Now()) != nil {
		t.Fatal("DaemonSet restart failed")
	}
	if _, err := Find(ctx, cs, "n", "nope"); !errors.Is(err, resolver.ErrNotFound) {
		t.Fatalf("nope: %v", err)
	}
}
