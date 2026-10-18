package forward

import (
	"bytes"
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestParsePorts(t *testing.T) {
	got, err := ParsePorts([]string{"3306", "13306:3306", "0:80"})
	want := []PortSpec{{3306, 3306}, {13306, 3306}, {0, 80}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v %v", got, err)
	}
	for _, bad := range []string{"x", "70000", "1:0", "1:", ":80", "0"} {
		if _, err := ParsePorts([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func podWithPorts(ports ...int32) *corev1.Pod {
	c := corev1.Container{}
	for _, p := range ports {
		c.Ports = append(c.Ports, corev1.ContainerPort{ContainerPort: p})
	}
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p"}, Spec: corev1.PodSpec{Containers: []corev1.Container{c}}}
}

func TestDefaultPorts(t *testing.T) {
	if got, err := DefaultPorts(podWithPorts(3306)); err != nil || got[0] != (PortSpec{3306, 3306}) {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := DefaultPorts(podWithPorts()); err == nil {
		t.Error("no ports accepted")
	}
	if _, err := DefaultPorts(podWithPorts(80, 443)); err == nil || !strings.Contains(err.Error(), "80") {
		t.Errorf("several ports: %v", err)
	}
}

func TestAssignLocalBusy(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	busy := l.Addr().(*net.TCPAddr).Port

	var note bytes.Buffer
	got, err := AssignLocal("127.0.0.1", []PortSpec{{busy, 3306}, {0, 80}}, &note)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Local == busy || got[0].Local == 0 || got[1].Local == 0 || got[0].Local == got[1].Local {
		t.Fatalf("bad assignment %v", got)
	}
	if got[0].Remote != 3306 || !strings.Contains(note.String(), "busy") {
		t.Fatalf("got %v note %q", got, note.String())
	}
}

// Supervisor behaviour, driven through the test seams.
type result struct {
	ready bool
	err   error
}

func runScripted(t *testing.T, results []result, resolveErr map[int]error) (err error, sleeps []time.Duration, errOut string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	calls, resolves := 0, 0
	f := &Forwarder{
		Err: &out,
		Resolve: func(context.Context) (*corev1.Pod, error) {
			resolves++
			return &corev1.Pod{}, resolveErr[resolves]
		},
		session: func(context.Context, *corev1.Pod) (bool, error) {
			r := results[calls]
			calls++
			if calls == len(results) && r.ready {
				cancel() // script exhausted: user hits Ctrl+C
			}
			return r.ready, r.err
		},
		sleep: func(_ context.Context, d time.Duration) bool { sleeps = append(sleeps, d); return true },
	}
	err = f.Run(ctx)
	return err, sleeps, out.String()
}

func TestReconnectsWithBackoff(t *testing.T) {
	lost := errors.New("lost connection to pod")
	err, sleeps, out := runScripted(t, []result{
		{true, lost}, {true, lost}, {true, lost}, {true, lost}, {true, nil},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}
	if !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("backoff %v, want %v", sleeps, want)
	}
	if !strings.Contains(out, "reconnecting") {
		t.Fatalf("no event line: %q", out)
	}
}

func TestBackoffCapped(t *testing.T) {
	var rs []result
	for i := 0; i < 12; i++ {
		rs = append(rs, result{true, errors.New("x")})
	}
	_, sleeps, _ := runScripted(t, rs, nil)
	if last := sleeps[len(sleeps)-1]; last != maxBackoff {
		t.Fatalf("last backoff %v, want cap %v", last, maxBackoff)
	}
}

func TestRetriesWhenPodMissingAfterFirstSuccess(t *testing.T) {
	// 2nd resolve fails (pod being replaced), 3rd works.
	err, sleeps, _ := runScripted(t, []result{{true, errors.New("lost")}, {true, nil}},
		map[int]error{2: errors.New("app not found")})
	if err != nil || len(sleeps) != 2 {
		t.Fatalf("err %v sleeps %v", err, sleeps)
	}
}

func TestFatalBeforeFirstSuccess(t *testing.T) {
	boom := errors.New("unable to listen")
	if err, sleeps, _ := runScripted(t, []result{{false, boom}}, nil); err != boom || len(sleeps) != 0 {
		t.Fatalf("session error: %v %v", err, sleeps)
	}
	notFound := errors.New("app not found")
	if err, _, _ := runScripted(t, []result{{false, nil}}, map[int]error{1: notFound}); err != notFound {
		t.Fatalf("resolve error: %v", err)
	}
}

func TestPodGone(t *testing.T) {
	mk := func(name string) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: name}}
	}
	cs := fake.NewSimpleClientset(mk("p"), mk("other"))

	// Deleting a different pod must not end the session.
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan bool, 1)
	go func() { res <- podGone(ctx, cs, "n", "p") }()
	time.Sleep(100 * time.Millisecond)
	cs.CoreV1().Pods("n").Delete(context.Background(), "other", metav1.DeleteOptions{})
	select {
	case <-res:
		t.Fatal("ended by an unrelated pod")
	case <-time.After(200 * time.Millisecond):
	}
	// Deleting ours does.
	cs.CoreV1().Pods("n").Delete(context.Background(), "p", metav1.DeleteOptions{})
	select {
	case gone := <-res:
		if !gone {
			t.Fatal("deletion not detected")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deletion not detected in time")
	}
	cancel()

	// Terminating (deletionTimestamp set, e.g. graceful shutdown) counts too.
	term := mk("t")
	now := metav1.Now()
	term.DeletionTimestamp = &now
	cs = fake.NewSimpleClientset(term)
	if !podGone(context.Background(), cs, "n", "t") {
		t.Fatal("terminating pod not detected")
	}

	// ctx cancel returns false.
	cs = fake.NewSimpleClientset(mk("live"))
	ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if podGone(ctx, cs, "n", "live") {
		t.Fatal("live pod reported gone")
	}
}
