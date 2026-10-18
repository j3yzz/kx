package debug

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestAdd(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "p"}})
	name, err := Add(context.Background(), cs, "n", "p", "busybox:1.36", "app")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := cs.CoreV1().Pods("n").Get(context.Background(), "p", metav1.GetOptions{})
	if len(p.Spec.EphemeralContainers) != 1 {
		t.Fatalf("got %+v", p.Spec.EphemeralContainers)
	}
	e := p.Spec.EphemeralContainers[0]
	if e.Name != name || !strings.HasPrefix(name, "kx-debug-") || e.Image != "busybox:1.36" ||
		e.TargetContainerName != "app" || !e.Stdin || !e.TTY {
		t.Errorf("bad container: %+v", e)
	}
	// A second debug session gets its own container.
	name2, _ := Add(context.Background(), cs, "n", "p", "busybox:1.36", "")
	if name2 == name {
		t.Error("names must be unique")
	}
	if _, err := Add(context.Background(), cs, "n", "missing", "x", ""); err == nil {
		t.Error("missing pod accepted")
	}
}

func podWith(state corev1.ContainerState) *fake.Clientset {
	return fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "p"},
		Status:     corev1.PodStatus{EphemeralContainerStatuses: []corev1.ContainerStatus{{Name: "dbg", State: state}}},
	})
}

func TestWaitRunning(t *testing.T) {
	run := podWith(corev1.ContainerState{Running: &corev1.ContainerStateRunning{}})
	if err := WaitRunning(context.Background(), run, "n", "p", "dbg", time.Second, 5*time.Millisecond); err != nil {
		t.Fatalf("running: %v", err)
	}

	pull := podWith(corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "nope"}})
	start := time.Now()
	err := WaitRunning(context.Background(), pull, "n", "p", "dbg", 5*time.Second, 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "ImagePullBackOff") || time.Since(start) > time.Second {
		t.Fatalf("pull failure should return at once: %v", err)
	}

	dead := podWith(corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1}})
	if err := WaitRunning(context.Background(), dead, "n", "p", "dbg", time.Second, 5*time.Millisecond); err == nil {
		t.Fatal("terminated accepted")
	}

	creating := podWith(corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}})
	err = WaitRunning(context.Background(), creating, "n", "p", "dbg", 50*time.Millisecond, 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("timeout: %v", err)
	}
}
