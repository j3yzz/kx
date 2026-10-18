package resolver

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func pod(ns, name string, age time.Duration, phase corev1.PodPhase, ready bool, labels map[string]string) *corev1.Pod {
	st := corev1.ConditionFalse
	if ready {
		st = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels,
			CreationTimestamp: metav1.NewTime(t0.Add(-age))},
		Status: corev1.PodStatus{Phase: phase, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: st}}},
	}
}

func sel(k, v string) *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: map[string]string{k: v}}
}

func names(pods []corev1.Pod) string {
	var n []string
	for _, p := range pods {
		n = append(n, p.Name)
	}
	return strings.Join(n, ",")
}

func TestResolve(t *testing.T) {
	cs := fake.NewSimpleClientset([]runtime.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "payments"},
			Spec: appsv1.DeploymentSpec{Selector: sel("tier", "pay")}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "mysql"},
			Spec: appsv1.StatefulSetSpec{Selector: sel("db", "mysql")}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "idle"},
			Spec: appsv1.DeploymentSpec{Selector: sel("x", "idle")}},
		// payments: workload selector differs from name labels on purpose
		pod("default", "payments-old", time.Hour, corev1.PodRunning, true, map[string]string{"tier": "pay"}),
		pod("default", "payments-new", time.Minute, corev1.PodRunning, true, map[string]string{"tier": "pay"}),
		pod("default", "mysql-0", time.Hour, corev1.PodRunning, true, map[string]string{"db": "mysql"}),
		pod("default", "web-1", time.Hour, corev1.PodRunning, true, map[string]string{"app.kubernetes.io/name": "web"}),
		pod("default", "cache-1", time.Hour, corev1.PodRunning, true, map[string]string{"app": "cache"}),
		pod("payments-stg", "payments-x", time.Hour, corev1.PodRunning, true, map[string]string{"app": "payments"}),
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "payments-prod", Name: "payments"}},
		pod("default", "rel-web-1", time.Hour, corev1.PodRunning, true, map[string]string{"app.kubernetes.io/instance": "rel", "app.kubernetes.io/name": "frontend"}),
		pod("default", "rel-worker-1", time.Hour, corev1.PodRunning, true, map[string]string{"app.kubernetes.io/instance": "rel", "app.kubernetes.io/name": "worker"}),
		pod("default", "other", time.Hour, corev1.PodRunning, true, map[string]string{"app": "other"}),
	}...)
	r := New(cs)
	ctx := context.Background()

	ok := []struct{ app, sel, want string }{
		{"payments", "", "payments-new,payments-old"}, // deployment selector, newest first
		{"mysql", "", "mysql-0"},                      // statefulset
		{"web", "", "web-1"},                          // app.kubernetes.io/name
		{"cache", "", "cache-1"},                      // app label
		{"whatever", "app=other", "other"},            // -l override
		{"rel", "", "rel-web-1,rel-worker-1"},         // Helm release via app.kubernetes.io/instance
	}
	for _, c := range ok {
		got, err := r.Resolve(ctx, "default", c.app, c.sel)
		if err != nil || names(got) != c.want {
			t.Errorf("%s: got %q err %v, want %q", c.app, names(got), err, c.want)
		}
	}

	_, err := r.Resolve(ctx, "default", "idle", "")
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "no pods") {
		t.Errorf("idle: %v", err)
	}

	_, err = r.Resolve(ctx, "default", "nope", "")
	if !errors.Is(err, ErrNotFound) || strings.Contains(err.Error(), "exists in") {
		t.Errorf("nope: %v", err)
	}
	// Dummy workload lookup with no match fell through to namespace hint:
	_, err = r.Resolve(ctx, "kube-system", "payments", "")
	want := "exists in default, payments-prod and payments-stg"
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), want) {
		t.Errorf("hint: %v, want containing %q", err, want)
	}
}

func TestPickPod(t *testing.T) {
	pods := []corev1.Pod{ // newest first
		*pod("n", "pending", time.Minute, corev1.PodPending, false, nil),
		*pod("n", "unready", 2*time.Minute, corev1.PodRunning, false, nil),
		*pod("n", "ready", 3*time.Minute, corev1.PodRunning, true, nil),
	}
	got, err := PickPod(pods, false, nil, nil)
	if err != nil || got.Name != "ready" {
		t.Fatalf("got %v %v", got, err)
	}
	got, _ = PickPod(pods[:2], false, nil, nil)
	if got.Name != "unready" {
		t.Fatalf("fallback: %s", got.Name)
	}
	if _, err = PickPod(pods[:1], false, nil, nil); err == nil || !strings.Contains(err.Error(), "pending=Pending") {
		t.Fatalf("none running: %v", err)
	}
	got, err = PickPod(pods, true, strings.NewReader("2\n"), &bytes.Buffer{})
	if err != nil || got.Name != "unready" {
		t.Fatalf("pick: %v %v", got, err)
	}
	if _, err = PickPod(pods, true, strings.NewReader("9\n"), &bytes.Buffer{}); err == nil {
		t.Fatal("bad selection accepted")
	}
}

func TestPickContainer(t *testing.T) {
	mk := func(ann string, cs ...string) *corev1.Pod {
		p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}}
		if ann != "" {
			p.Annotations = map[string]string{defaultContainerAnnotation: ann}
		}
		for _, c := range cs {
			p.Spec.Containers = append(p.Spec.Containers, corev1.Container{Name: c})
		}
		return p
	}
	out := &bytes.Buffer{}
	cases := []struct {
		name string
		pod  *corev1.Pod
		flag string
		in   string
		want string
		fail bool
	}{
		{"flag", mk("", "a", "b"), "b", "", "b", false},
		{"bad flag", mk("", "a", "b"), "z", "", "", true},
		{"annotation", mk("b", "a", "b"), "", "", "b", false},
		{"single", mk("", "a"), "", "", "a", false},
		{"interactive", mk("", "a", "b"), "", "2\n", "b", false},
		{"stale annotation", mk("gone", "a", "b"), "", "1\n", "a", false},
	}
	for _, c := range cases {
		got, err := PickContainer(c.pod, c.flag, strings.NewReader(c.in), out)
		if (err != nil) != c.fail || got != c.want {
			t.Errorf("%s: got %q err %v", c.name, got, err)
		}
	}
}

func TestSeveralComponents(t *testing.T) {
	web := *pod("n", "web-1", time.Minute, corev1.PodRunning, true, map[string]string{componentLabel: "web"})
	web2 := *pod("n", "web-0", time.Hour, corev1.PodRunning, true, map[string]string{componentLabel: "web"})
	wk := *pod("n", "wk-1", 2*time.Minute, corev1.PodRunning, true, map[string]string{componentLabel: "worker"})
	pods := []corev1.Pod{web, wk, web2}

	if _, err := PickPod(pods, false, nil, nil); err == nil || !strings.Contains(err.Error(), "web, worker") {
		t.Fatalf("want components error, got %v", err)
	}
	if got, err := PickPod([]corev1.Pod{web, web2}, false, nil, nil); err != nil || got.Name != "web-1" {
		t.Fatalf("single component: %v %v", got, err)
	}
	if got, err := PickPod(pods, true, strings.NewReader("2\n"), &bytes.Buffer{}); err != nil || got.Name != "wk-1" {
		t.Fatalf("--pick should bypass: %v %v", got, err)
	}
	if rep := Representatives(pods); names(rep) != "web-1,wk-1" {
		t.Fatalf("representatives: %s", names(rep))
	}
}

func TestApps(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "payments"}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "mysql"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "other", Name: "elsewhere"}},
		pod("n", "p1", time.Hour, corev1.PodRunning, true, map[string]string{"app.kubernetes.io/instance": "rel"}),
		pod("n", "p2", time.Hour, corev1.PodRunning, true, map[string]string{"app.kubernetes.io/instance": "rel"}),
	)
	got, err := New(cs).Apps(context.Background(), "n")
	if err != nil || strings.Join(got, ",") != "mysql,payments,rel" {
		t.Fatalf("got %v %v", got, err)
	}
}
