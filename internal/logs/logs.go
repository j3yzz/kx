// Package logs streams the logs of every pod of an app, one prefixed (and, on
// a terminal, colored) line at a time.
package logs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// Source returns the app's current pods (the Resolver, in practice).
type Source func(ctx context.Context) ([]corev1.Pod, error)

type Options struct {
	Container        string // default: default-container annotation, else the first container
	Follow, Previous bool
	Since            time.Duration // 0 = all
	Tail             int64         // lines per pod; <0 = all
	Color            bool
	PollEvery        time.Duration // follow: how often to look for new pods (default 2s)
}

var palette = []string{"36", "33", "32", "35", "34", "96", "93", "92"} // no red: errors own it

type printer struct {
	mu    sync.Mutex
	out   io.Writer
	color bool
	next  int
	hues  map[types.UID]string
}

func (p *printer) prefix(pod *corev1.Pod) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	h, ok := p.hues[pod.UID]
	if !ok {
		h = palette[p.next%len(palette)]
		p.next++
		p.hues[pod.UID] = h
	}
	if p.color {
		return "\x1b[" + h + "m[" + pod.Name + "]\x1b[0m "
	}
	return "[" + pod.Name + "] "
}

func (p *printer) line(prefix, l string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(l) == 0 || l[len(l)-1] != '\n' {
		l += "\n"
	}
	io.WriteString(p.out, prefix+l)
}

// Run prints the logs. Without Follow, pods are printed one after another so
// each pod's lines stay together; with Follow they stream concurrently and
// pods that appear later (rollouts) are picked up.
func Run(ctx context.Context, cs kubernetes.Interface, src Source, o Options, out, errOut io.Writer) error {
	if o.Follow && o.Previous {
		return fmt.Errorf("--previous cannot be combined with --follow")
	}
	pods, err := src(ctx)
	if err != nil {
		return err
	}
	sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
	pr := &printer{out: out, color: o.Color, hues: map[types.UID]string{}}

	if !o.Follow {
		var first error
		failed := 0
		for i := range pods {
			if err := streamPod(ctx, cs, &pods[i], o, pr); err != nil && ctx.Err() == nil {
				failed++
				if first == nil {
					first = err
				}
				fmt.Fprintf(errOut, "%s: %v\n", pods[i].Name, err)
			}
		}
		if failed == len(pods) {
			return first
		}
		return nil
	}

	var wg sync.WaitGroup
	seen := map[types.UID]bool{}
	start := func(ps []corev1.Pod) {
		for i := range ps {
			p := ps[i]
			if seen[p.UID] {
				continue
			}
			seen[p.UID] = true
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := streamPod(ctx, cs, &p, o, pr); err != nil && ctx.Err() == nil {
					fmt.Fprintf(errOut, "%s: %v\n", p.Name, err)
				}
			}()
		}
	}
	start(pods)
	every := o.PollEvery
	if every == 0 {
		every = 2 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		case <-t.C:
			// Errors here are the app mid-rollout; keep what we have.
			if ps, err := src(ctx); err == nil {
				start(ps)
			}
		}
	}
}

func streamPod(ctx context.Context, cs kubernetes.Interface, pod *corev1.Pod, o Options, pr *printer) error {
	opts := &corev1.PodLogOptions{
		Container: container(pod, o.Container),
		Follow:    o.Follow,
		Previous:  o.Previous,
	}
	if o.Since > 0 {
		s := int64(o.Since.Seconds())
		opts.SinceSeconds = &s
	}
	if o.Tail >= 0 {
		opts.TailLines = &o.Tail
	}
	rc, err := cs.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, opts).Stream(ctx)
	if err != nil {
		return err
	}
	defer rc.Close()

	prefix := pr.prefix(pod)
	br := bufio.NewReader(rc) // ReadString has no line-length limit, unlike Scanner
	for {
		l, err := br.ReadString('\n')
		if l != "" {
			pr.line(prefix, l)
		}
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

func container(pod *corev1.Pod, flag string) string {
	if flag != "" {
		return flag
	}
	if d := pod.Annotations["kubectl.kubernetes.io/default-container"]; d != "" {
		return d
	}
	if len(pod.Spec.Containers) > 0 {
		return pod.Spec.Containers[0].Name
	}
	return ""
}
