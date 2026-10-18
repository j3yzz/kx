package logs

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func pod(name string) corev1.Pod {
	return corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: name, UID: types.UID(name)},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}, {Name: "sidecar"}}}}
}

func fixed(ps ...corev1.Pod) Source {
	return func(context.Context) ([]corev1.Pod, error) { return append([]corev1.Pod{}, ps...), nil }
}

func objs(ps ...corev1.Pod) *fake.Clientset {
	cs := fake.NewSimpleClientset()
	for i := range ps {
		cs.CoreV1().Pods("n").Create(context.Background(), &ps[i], metav1.CreateOptions{})
	}
	return cs
}

func TestRunPrefixesAndGroupsPods(t *testing.T) {
	ps := []corev1.Pod{pod("b-2"), pod("a-1")}
	var out, errb bytes.Buffer
	if err := Run(context.Background(), objs(ps...), fixed(ps...), Options{Tail: -1}, &out, &errb); err != nil {
		t.Fatal(err)
	}
	// fake clientset serves the text "fake logs"; sorted by pod name
	want := "[a-1] fake logs\n[b-2] fake logs\n"
	if out.String() != want {
		t.Fatalf("got %q want %q", out.String(), want)
	}
}

func TestColorOnlyWhenAsked(t *testing.T) {
	ps := []corev1.Pod{pod("a"), pod("b")}
	var plain, colored bytes.Buffer
	Run(context.Background(), objs(ps...), fixed(ps...), Options{Tail: -1}, &plain, io_discard{})
	Run(context.Background(), objs(ps...), fixed(ps...), Options{Tail: -1, Color: true}, &colored, io_discard{})
	if strings.Contains(plain.String(), "\x1b") {
		t.Error("color leaked into plain output")
	}
	lines := strings.Split(strings.TrimSpace(colored.String()), "\n")
	if len(lines) != 2 || lines[0][:5] == lines[1][:5] {
		t.Errorf("pods should get different colors: %q", lines)
	}
}

type io_discard struct{}

func (io_discard) Write(p []byte) (int, error) { return len(p), nil }

func TestPreviousWithFollowRejected(t *testing.T) {
	if err := Run(context.Background(), objs(), fixed(), Options{Follow: true, Previous: true}, io_discard{}, io_discard{}); err == nil {
		t.Fatal("accepted")
	}
}

func TestFollowPicksUpNewPods(t *testing.T) {
	first, second := pod("old"), pod("new")
	cs := objs(first, second)
	var mu sync.Mutex
	calls := 0
	src := func(context.Context) ([]corev1.Pod, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return []corev1.Pod{first}, nil
		}
		return []corev1.Pod{first, second}, nil // rollout: a pod appeared
	}
	ctx, cancel := context.WithCancel(context.Background())
	var out syncBuf
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cs, src, Options{Follow: true, Tail: -1, PollEvery: 20 * time.Millisecond}, &out, io_discard{})
	}()
	deadline := time.After(2 * time.Second)
	for !(strings.Contains(out.String(), "[old]") && strings.Contains(out.String(), "[new]")) {
		select {
		case <-deadline:
			t.Fatalf("new pod not streamed: %q", out.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out.String(), "[old]"); n != 1 {
		t.Errorf("old pod streamed %d times", n)
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestContainerChoice(t *testing.T) {
	p := pod("p")
	if container(&p, "") != "app" {
		t.Error("first container expected")
	}
	p.Annotations = map[string]string{"kubectl.kubernetes.io/default-container": "sidecar"}
	if container(&p, "") != "sidecar" || container(&p, "x") != "x" {
		t.Error("annotation then flag precedence")
	}
}
