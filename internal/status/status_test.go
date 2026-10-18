package status

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

var now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func owner(kind, name string) []metav1.OwnerReference {
	t := true
	return []metav1.OwnerReference{{Kind: kind, Name: name, Controller: &t}}
}

func fixture() (*fake.Clientset, []corev1.Pod) {
	two := int32(2)
	crashing := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "web-a", OwnerReferences: owner("ReplicaSet", "web-rs"),
			CreationTimestamp: metav1.NewTime(now.Add(-3 * time.Hour))},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
			Ready: false, RestartCount: 4,
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Reason: "OOMKilled", ExitCode: 137, FinishedAt: metav1.NewTime(now.Add(-3 * time.Minute))}},
		}}},
	}
	healthy := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "web-b", OwnerReferences: owner("ReplicaSet", "web-rs"),
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute))},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Ready: true}}},
	}
	cs := fake.NewSimpleClientset(
		&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "web-rs", OwnerReferences: owner("Deployment", "web")}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "web"},
			Spec: appsv1.DeploymentSpec{Replicas: &two}, Status: appsv1.DeploymentStatus{ReadyReplicas: 1, UpdatedReplicas: 2}},
		&corev1.Event{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "e1"}, Type: "Warning", Reason: "BackOff", Message: "Back-off restarting failed container",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "web-a"}, LastTimestamp: metav1.NewTime(now.Add(-1 * time.Minute)), Count: 9},
		&corev1.Event{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "e2"}, Type: "Normal", Reason: "Pulled",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "web-b"}, LastTimestamp: metav1.NewTime(now.Add(-8 * time.Minute))},
		&corev1.Event{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "e3"}, Type: "Warning", Reason: "Unrelated",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "other-pod"}, LastTimestamp: metav1.NewTime(now)},
	)
	return cs, []corev1.Pod{healthy, crashing}
}

func TestCollect(t *testing.T) {
	cs, pods := fixture()
	r := Collect(context.Background(), cs, "n", pods, now, 10)

	if len(r.Workloads) != 1 || r.Workloads[0] != (Workload{"Deployment", "web", 2, 1, 2}) {
		t.Errorf("workloads: %+v", r.Workloads)
	}
	if len(r.Pods) != 2 || r.Pods[0].Name != "web-a" {
		t.Fatalf("pods sorted by name: %+v", r.Pods)
	}
	a := r.Pods[0]
	if a.Restarts != 4 || a.Ready != "0/1" || a.Age != "3h" ||
		a.Reason != "CrashLoopBackOff; last: OOMKilled (exit 137) 3m ago" {
		t.Errorf("crashing pod: %+v", a)
	}
	if b := r.Pods[1]; b.Reason != "" || b.Ready != "1/1" {
		t.Errorf("healthy pod: %+v", b)
	}
	if len(r.Events) != 2 || r.Events[0].Reason != "BackOff" || r.Events[0].Age != "1m" || r.Events[0].Count != 9 {
		t.Errorf("events (newest first, only this app's pods): %+v", r.Events)
	}
	if got := Collect(context.Background(), cs, "n", pods, now, 1).Events; len(got) != 1 {
		t.Errorf("max events not applied: %d", len(got))
	}
}

func TestRender(t *testing.T) {
	cs, pods := fixture()
	r := Collect(context.Background(), cs, "n", pods, now, 10)
	var b bytes.Buffer
	if err := Render(&b, r, "table"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"deployment/web", "1/2", "OOMKilled", "BackOff"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("table lacks %q:\n%s", want, b.String())
		}
	}
	b.Reset()
	if err := Render(&b, r, "json"); err != nil || !strings.Contains(b.String(), `"restarts": 4`) {
		t.Errorf("json: %v\n%s", err, b.String())
	}
}

func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{-time.Second: "0s", 45 * time.Second: "45s", 3 * time.Minute: "3m", 5 * time.Hour: "5h", 72 * time.Hour: "3d"} {
		if got := Age(d); got != want {
			t.Errorf("%v: %s want %s", d, got, want)
		}
	}
}
