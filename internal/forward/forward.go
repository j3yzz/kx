// Package forward keeps a port-forward tunnel to an app alive across pod
// restarts: when the tunnel drops it re-resolves the app's pod and reconnects
// with exponential backoff.
package forward

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/j3yzz/kx/internal/kube"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

const (
	initialBackoff = 500 * time.Millisecond
	maxBackoff     = 30 * time.Second
	// A session that lasted this long was healthy, so the next failure starts
	// over at initialBackoff instead of continuing to grow.
	stableAfter = 10 * time.Second
)

type Forwarder struct {
	Client  *kube.Client
	App     string
	Ports   []PortSpec // local ports already assigned (see AssignLocal)
	Address string     // local bind address, e.g. 127.0.0.1
	Resolve func(ctx context.Context) (*corev1.Pod, error)
	Out     io.Writer // "localhost:3306 -> app/pod:3306" lines
	Err     io.Writer // reconnect events and forwarding errors
	// OnFirstReady, if set, is called once when the tunnel first comes up.
	OnFirstReady func()
	once         sync.Once

	// Test seams.
	session func(ctx context.Context, pod *corev1.Pod) (ready bool, err error)
	sleep   func(ctx context.Context, d time.Duration) bool
	now     func() time.Time
}

// Run blocks until ctx is cancelled. Before the tunnel has come up once, any
// error is returned (wrong app, no permission, port can't be bound). After
// that, everything is retried.
func (f *Forwarder) Run(ctx context.Context) error {
	if f.session == nil {
		f.session = f.dial
	}
	if f.sleep == nil {
		f.sleep = sleep
	}
	if f.now == nil {
		f.now = time.Now
	}

	backoff := initialBackoff
	everReady := false
	for ctx.Err() == nil {
		start := f.now()
		pod, err := f.Resolve(ctx)
		if err == nil {
			var ready bool
			ready, err = f.session(ctx, pod)
			everReady = everReady || ready
			if ready && f.now().Sub(start) >= stableAfter {
				backoff = initialBackoff
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		if !everReady {
			return err
		}
		fmt.Fprintf(f.Err, "tunnel lost (%v); reconnecting in %s\n", describe(err), backoff)
		if !f.sleep(ctx, backoff) {
			return nil
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	return nil
}

func describe(err error) string {
	if err == nil {
		return "connection closed"
	}
	return err.Error()
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// dial runs one tunnel until it drops. ready reports whether it ever came up.
func (f *Forwarder) dial(ctx context.Context, pod *corev1.Pod) (bool, error) {
	cfg := f.Client.Config
	req := f.Client.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(pod.Namespace).Name(pod.Name).SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return false, err
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, req.URL())
	if tunnel, err := portforward.NewSPDYOverWebsocketDialer(req.URL(), cfg); err == nil {
		dialer = portforward.NewFallbackDialer(tunnel, dialer, func(err error) bool {
			return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
		})
	}

	ports := make([]string, len(f.Ports))
	for i, p := range f.Ports {
		ports[i] = strconv.Itoa(p.Local) + ":" + strconv.Itoa(p.Remote)
	}
	// End the session as soon as the pod goes away; see podGone.
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var gone atomic.Bool
	go func() {
		if podGone(sctx, f.Client, pod.Namespace, pod.Name) {
			gone.Store(true)
			cancel()
		}
	}()

	ready := make(chan struct{})
	pf, err := portforward.NewOnAddressesWithContext(sctx, dialer, []string{f.Address}, ports, ready, io.Discard, f.Err)
	if err != nil {
		return false, err
	}
	go func() {
		select {
		case <-ready:
			f.announce(pod)
			f.once.Do(func() {
				if f.OnFirstReady != nil {
					f.OnFirstReady()
				}
			})
		case <-sctx.Done():
		}
	}()

	err = pf.ForwardPorts()
	if gone.Load() {
		err = fmt.Errorf("pod %s was deleted or is terminating", pod.Name)
	}
	select {
	case <-ready:
		return true, err
	default:
		return false, err
	}
}

func (f *Forwarder) announce(pod *corev1.Pod) {
	host := f.Address
	if host == "127.0.0.1" {
		host = "localhost"
	}
	for _, p := range f.Ports {
		fmt.Fprintf(f.Out, "%s:%d -> %s/%s:%d\n", host, p.Local, f.App, pod.Name, p.Remote)
	}
}
