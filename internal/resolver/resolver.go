// Package resolver turns an application name into pods. It is the only place
// in kx that knows how an app maps to pods.
package resolver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/j3yzz/kx/internal/kube"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ErrNotFound matches (errors.Is) any "app not found" failure; cmd maps it to exit code 2.
var ErrNotFound = errors.New("app not found")

type notFoundError string

func (e notFoundError) Error() string        { return string(e) }
func (e notFoundError) Is(target error) bool { return target == ErrNotFound }

func notFoundf(format string, a ...any) error { return notFoundError(fmt.Sprintf(format, a...)) }

const defaultContainerAnnotation = "kubectl.kubernetes.io/default-container"

var appLabels = []string{"app.kubernetes.io/name", "app"}

type Resolver struct{ cs kubernetes.Interface }

func New(cs kubernetes.Interface) *Resolver { return &Resolver{cs} }

// Resolve returns the pods of app in ns, newest first. Lookup order: the
// -l selector if given; else a Deployment, StatefulSet or DaemonSet named app
// (using its selector); else pods labelled app.kubernetes.io/name or app.
func (r *Resolver) Resolve(ctx context.Context, ns, app, selector string) ([]corev1.Pod, error) {
	if selector != "" {
		pods, err := r.pods(ctx, ns, selector)
		if err == nil && len(pods) == 0 {
			err = notFoundf("no pods match -l %s in namespace %s", selector, ns)
		}
		return pods, err
	}

	sel, found, err := r.workloadSelector(ctx, ns, app)
	if err != nil {
		return nil, err
	}
	if found {
		pods, err := r.pods(ctx, ns, sel)
		if err == nil && len(pods) == 0 {
			err = notFoundf("%s exists in namespace %s but has no pods (scaled to 0?)", app, ns)
		}
		return pods, err
	}

	for _, l := range appLabels {
		pods, err := r.pods(ctx, ns, l+"="+app)
		if err != nil {
			return nil, err
		}
		if len(pods) > 0 {
			return pods, nil
		}
	}
	return nil, r.notFound(ctx, ns, app)
}

func (r *Resolver) pods(ctx context.Context, ns, selector string) ([]corev1.Pod, error) {
	l, err := r.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, kube.WrapError(err)
	}
	sort.Slice(l.Items, func(i, j int) bool {
		return l.Items[i].CreationTimestamp.After(l.Items[j].CreationTimestamp.Time)
	})
	return l.Items, nil
}

// workloadSelector returns the label selector of the first workload named app.
func (r *Resolver) workloadSelector(ctx context.Context, ns, app string) (string, bool, error) {
	getters := []func() (*metav1.LabelSelector, error){
		func() (*metav1.LabelSelector, error) {
			o, err := r.cs.AppsV1().Deployments(ns).Get(ctx, app, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			return o.Spec.Selector, nil
		},
		func() (*metav1.LabelSelector, error) {
			o, err := r.cs.AppsV1().StatefulSets(ns).Get(ctx, app, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			return o.Spec.Selector, nil
		},
		func() (*metav1.LabelSelector, error) {
			o, err := r.cs.AppsV1().DaemonSets(ns).Get(ctx, app, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			return o.Spec.Selector, nil
		},
	}
	for _, get := range getters {
		ls, err := get()
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return "", false, kube.WrapError(err)
		}
		s, err := metav1.LabelSelectorAsSelector(ls)
		if err != nil {
			return "", false, err
		}
		return s.String(), true, nil
	}
	return "", false, nil
}

// notFound builds the error, hinting at other namespaces that have the app.
// Cluster-wide list failures (e.g. 403) just drop the hint.
func (r *Resolver) notFound(ctx context.Context, ns, app string) error {
	found := map[string]bool{}
	fs := metav1.ListOptions{FieldSelector: "metadata.name=" + app}
	apps := r.cs.AppsV1()
	all := metav1.NamespaceAll
	if l, err := apps.Deployments(all).List(ctx, fs); err == nil {
		for _, o := range l.Items {
			found[o.Namespace] = found[o.Namespace] || o.Name == app
		}
	}
	if l, err := apps.StatefulSets(all).List(ctx, fs); err == nil {
		for _, o := range l.Items {
			found[o.Namespace] = found[o.Namespace] || o.Name == app
		}
	}
	if l, err := apps.DaemonSets(all).List(ctx, fs); err == nil {
		for _, o := range l.Items {
			found[o.Namespace] = found[o.Namespace] || o.Name == app
		}
	}
	for _, lb := range appLabels {
		if l, err := r.cs.CoreV1().Pods(all).List(ctx, metav1.ListOptions{LabelSelector: lb + "=" + app}); err == nil {
			for _, p := range l.Items {
				found[p.Namespace] = true
			}
		}
	}

	var names []string
	for n, ok := range found {
		if ok && n != ns {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return notFoundf("app %s not found in namespace %s; try -l <selector> or check --context", app, ns)
	}
	return notFoundf("app %s not found in namespace %s; it exists in %s (use -n)", app, ns, joinAnd(names))
}

func joinAnd(s []string) string {
	if len(s) == 1 {
		return s[0]
	}
	return strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1]
}

// PickPod chooses one pod from pods (newest first, as Resolve returns them):
// the newest Running+Ready pod, else the newest Running one. With pick, the
// user chooses from a list on in/out instead.
func PickPod(pods []corev1.Pod, pick bool, in io.Reader, out io.Writer) (*corev1.Pod, error) {
	if pick {
		items := make([]string, len(pods))
		for i, p := range pods {
			items[i] = fmt.Sprintf("%s (%s)", p.Name, podStatus(&p))
		}
		i, err := choose(in, out, "Pods:", items)
		if err != nil {
			return nil, err
		}
		return &pods[i], nil
	}
	var running *corev1.Pod
	for i := range pods {
		p := &pods[i]
		if p.Status.Phase != corev1.PodRunning || p.DeletionTimestamp != nil {
			continue
		}
		if podReady(p) {
			return p, nil
		}
		if running == nil {
			running = p
		}
	}
	if running != nil {
		return running, nil
	}
	return nil, fmt.Errorf("no Running pod (found: %s); use --pick to choose anyway", statusList(pods))
}

// PickContainer chooses a container: name if given, else the default-container
// annotation, else the only container, else an interactive choice.
func PickContainer(pod *corev1.Pod, name string, in io.Reader, out io.Writer) (string, error) {
	names := make([]string, len(pod.Spec.Containers))
	for i, c := range pod.Spec.Containers {
		names[i] = c.Name
	}
	has := func(n string) bool {
		for _, c := range names {
			if c == n {
				return true
			}
		}
		return false
	}
	if name != "" {
		if !has(name) {
			return "", fmt.Errorf("container %q not in pod %s (containers: %s)", name, pod.Name, strings.Join(names, ", "))
		}
		return name, nil
	}
	if d := pod.Annotations[defaultContainerAnnotation]; d != "" && has(d) {
		return d, nil
	}
	if len(names) == 1 {
		return names[0], nil
	}
	i, err := choose(in, out, "Containers in "+pod.Name+":", names)
	if err != nil {
		return "", err
	}
	return names[i], nil
}

func choose(in io.Reader, out io.Writer, title string, items []string) (int, error) {
	fmt.Fprintln(out, title)
	for i, it := range items {
		fmt.Fprintf(out, "  %d) %s\n", i+1, it)
	}
	fmt.Fprintf(out, "Choose [1-%d]: ", len(items))
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return 0, errors.New("no selection made")
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(items) {
		return 0, fmt.Errorf("invalid selection %q", strings.TrimSpace(line))
	}
	return n - 1, nil
}

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podStatus(p *corev1.Pod) string {
	switch {
	case p.DeletionTimestamp != nil:
		return "Terminating"
	case p.Status.Phase == corev1.PodRunning && !podReady(p):
		return "Running, not ready"
	}
	return string(p.Status.Phase)
}

func statusList(pods []corev1.Pod) string {
	s := make([]string, len(pods))
	for i := range pods {
		s[i] = pods[i].Name + "=" + podStatus(&pods[i])
	}
	return strings.Join(s, ", ")
}
