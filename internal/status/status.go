// Package status summarizes an app's health: workload replicas, per-pod
// restarts and last termination reason (OOMKilled, ...), and recent events.
package status

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Workload struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Desired int32  `json:"desired"`
	Ready   int32  `json:"ready"`
	Updated int32  `json:"updated"`
}

type Pod struct {
	Name     string `json:"name"`
	Phase    string `json:"phase"`
	Ready    string `json:"ready"` // "1/2" containers
	Restarts int32  `json:"restarts"`
	Age      string `json:"age"`
	Reason   string `json:"reason,omitempty"` // current waiting reason and/or last termination
}

type Event struct {
	Type    string `json:"type"`
	Reason  string `json:"reason"`
	Object  string `json:"object"`
	Message string `json:"message"`
	Age     string `json:"age"`
	Count   int32  `json:"count"`
}

type Report struct {
	Workloads []Workload `json:"workloads"`
	Pods      []Pod      `json:"pods"`
	Events    []Event    `json:"events"`
}

// Collect builds the report for pods (all pods of the app) in ns. Workload and
// event lookups that fail (no permission, ...) leave those sections empty
// rather than failing the whole report.
func Collect(ctx context.Context, cs kubernetes.Interface, ns string, pods []corev1.Pod, now time.Time, maxEvents int) *Report {
	r := &Report{Workloads: []Workload{}, Pods: []Pod{}, Events: []Event{}}
	names := map[string]bool{}
	seen := map[string]bool{}
	sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
	for i := range pods {
		p := &pods[i]
		names[p.Name] = true
		r.Pods = append(r.Pods, podStatus(p, now))
		if w, ok := workload(ctx, cs, ns, p); ok && !seen[w.Kind+"/"+w.Name] {
			seen[w.Kind+"/"+w.Name] = true
			r.Workloads = append(r.Workloads, w)
		}
	}
	sort.Slice(r.Workloads, func(i, j int) bool { return r.Workloads[i].Name < r.Workloads[j].Name })
	r.Events = events(ctx, cs, ns, names, now, maxEvents)
	return r
}

func podStatus(p *corev1.Pod, now time.Time) Pod {
	phase := string(p.Status.Phase)
	if p.DeletionTimestamp != nil {
		phase = "Terminating"
	}
	var ready, total int
	var restarts int32
	var waiting []string
	var last *corev1.ContainerStateTerminated
	statuses := append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
	for _, cs := range statuses {
		total++
		if cs.Ready {
			ready++
		}
		restarts += cs.RestartCount
		if w := cs.State.Waiting; w != nil && w.Reason != "" && w.Reason != "PodInitializing" && w.Reason != "ContainerCreating" {
			waiting = append(waiting, w.Reason)
		}
		if t := cs.LastTerminationState.Terminated; t != nil && (last == nil || t.FinishedAt.After(last.FinishedAt.Time)) {
			last = t
		}
	}
	var parts []string
	if len(waiting) > 0 {
		parts = append(parts, strings.Join(dedupe(waiting), ","))
	}
	if last != nil {
		s := fmt.Sprintf("last: %s (exit %d)", last.Reason, last.ExitCode)
		if last.Reason == "" {
			s = fmt.Sprintf("last: exit %d", last.ExitCode)
		}
		if !last.FinishedAt.IsZero() {
			s += " " + Age(now.Sub(last.FinishedAt.Time)) + " ago"
		}
		parts = append(parts, s)
	}
	return Pod{
		Name: p.Name, Phase: phase, Ready: fmt.Sprintf("%d/%d", ready, total), Restarts: restarts,
		Age: Age(now.Sub(p.CreationTimestamp.Time)), Reason: strings.Join(parts, "; "),
	}
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// workload finds the Deployment/StatefulSet/DaemonSet controlling p.
func workload(ctx context.Context, cs kubernetes.Interface, ns string, p *corev1.Pod) (Workload, bool) {
	o := metav1.GetControllerOf(p)
	if o == nil {
		return Workload{}, false
	}
	if o.Kind == "ReplicaSet" {
		rs, err := cs.AppsV1().ReplicaSets(ns).Get(ctx, o.Name, metav1.GetOptions{})
		if err != nil {
			return Workload{}, false
		}
		if o = metav1.GetControllerOf(rs); o == nil {
			return Workload{}, false
		}
	}
	switch o.Kind {
	case "Deployment":
		d, err := cs.AppsV1().Deployments(ns).Get(ctx, o.Name, metav1.GetOptions{})
		if err != nil {
			return Workload{}, false
		}
		return Workload{"Deployment", d.Name, replicas(d.Spec.Replicas), d.Status.ReadyReplicas, d.Status.UpdatedReplicas}, true
	case "StatefulSet":
		s, err := cs.AppsV1().StatefulSets(ns).Get(ctx, o.Name, metav1.GetOptions{})
		if err != nil {
			return Workload{}, false
		}
		return Workload{"StatefulSet", s.Name, replicas(s.Spec.Replicas), s.Status.ReadyReplicas, s.Status.UpdatedReplicas}, true
	case "DaemonSet":
		d, err := cs.AppsV1().DaemonSets(ns).Get(ctx, o.Name, metav1.GetOptions{})
		if err != nil {
			return Workload{}, false
		}
		return daemon(d), true
	}
	return Workload{}, false
}

func daemon(d *appsv1.DaemonSet) Workload {
	return Workload{"DaemonSet", d.Name, d.Status.DesiredNumberScheduled, d.Status.NumberReady, d.Status.UpdatedNumberScheduled}
}

func replicas(r *int32) int32 {
	if r == nil {
		return 1
	}
	return *r
}

// events returns the newest events about the given pods.
func events(ctx context.Context, cs kubernetes.Interface, ns string, pods map[string]bool, now time.Time, max int) []Event {
	l, err := cs.CoreV1().Events(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return []Event{}
	}
	type ev struct {
		e  corev1.Event
		at time.Time
	}
	var mine []ev
	for _, e := range l.Items {
		if e.InvolvedObject.Kind == "Pod" && pods[e.InvolvedObject.Name] {
			at := e.LastTimestamp.Time
			if at.IsZero() {
				at = e.EventTime.Time
			}
			if at.IsZero() {
				at = e.CreationTimestamp.Time
			}
			mine = append(mine, ev{e, at})
		}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].at.After(mine[j].at) })
	if len(mine) > max {
		mine = mine[:max]
	}
	out := make([]Event, len(mine))
	for i, m := range mine {
		out[i] = Event{m.e.Type, m.e.Reason, m.e.InvolvedObject.Name, strings.TrimSpace(m.e.Message), Age(now.Sub(m.at)), m.e.Count}
	}
	return out
}

// Age formats d compactly: 45s, 3m, 2h, 5d.
func Age(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// Render writes the report as "table" or "json".
func Render(w io.Writer, r *Report, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	case "table":
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		if len(r.Workloads) > 0 {
			fmt.Fprintln(tw, "WORKLOAD\tREADY\tUP-TO-DATE")
			for _, x := range r.Workloads {
				fmt.Fprintf(tw, "%s/%s\t%d/%d\t%d\n", strings.ToLower(x.Kind), x.Name, x.Ready, x.Desired, x.Updated)
			}
			fmt.Fprintln(tw)
		}
		fmt.Fprintln(tw, "POD\tPHASE\tREADY\tRESTARTS\tAGE\tNOTES")
		for _, p := range r.Pods {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", p.Name, p.Phase, p.Ready, p.Restarts, p.Age, p.Reason)
		}
		if len(r.Events) > 0 {
			fmt.Fprintln(tw)
			fmt.Fprintln(tw, "EVENTS\t\t\t\t\t")
			fmt.Fprintln(tw, "AGE\tTYPE\tREASON\tOBJECT\tMESSAGE\t")
			for _, e := range r.Events {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t\n", e.Age, e.Type, e.Reason, e.Object, e.Message)
			}
		}
		return tw.Flush()
	}
	return fmt.Errorf("unknown output format %q", format)
}
