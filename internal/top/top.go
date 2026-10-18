// Package top shows CPU and memory usage per pod next to requests and limits.
// Usage comes from the metrics API (metrics-server), called directly so kx
// needs no extra dependency.
package top

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes"
)

// ErrNoMetrics means the cluster has no usable metrics API.
var ErrNoMetrics = errors.New("metrics API not available (is metrics-server installed?)")

// PodMetrics is the part of metrics.k8s.io/v1beta1 PodMetrics we read.
type PodMetrics struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Containers []struct {
		Name  string `json:"name"`
		Usage struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
		} `json:"usage"`
	} `json:"containers"`
}

// Fetch returns the pod metrics of namespace ns.
func Fetch(ctx context.Context, cs kubernetes.Interface, ns string) ([]PodMetrics, error) {
	raw, err := cs.CoreV1().RESTClient().Get().
		AbsPath("/apis/metrics.k8s.io/v1beta1/namespaces/" + ns + "/pods").DoRaw(ctx)
	if err != nil {
		if apierrors.IsNotFound(err) || apierrors.IsServiceUnavailable(err) {
			return nil, ErrNoMetrics
		}
		return nil, err
	}
	var l struct {
		Items []PodMetrics `json:"items"`
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, fmt.Errorf("decode metrics: %w", err)
	}
	return l.Items, nil
}

// Row is one pod's usage next to what it asked for. Quantities are in
// millicores and bytes; a zero request/limit means "not set".
type Row struct {
	Pod        string `json:"pod"`
	CPU        int64  `json:"cpuMilli"`
	CPURequest int64  `json:"cpuRequestMilli"`
	CPULimit   int64  `json:"cpuLimitMilli"`
	Memory     int64  `json:"memoryBytes"`
	MemRequest int64  `json:"memoryRequestBytes"`
	MemLimit   int64  `json:"memoryLimitBytes"`
	NoMetrics  bool   `json:"noMetrics,omitempty"` // pod too new for metrics-server
}

// Build joins pods with their metrics, sorted by pod name.
func Build(pods []corev1.Pod, metrics []PodMetrics) ([]Row, error) {
	byPod := map[string]PodMetrics{}
	for _, m := range metrics {
		byPod[m.Metadata.Name] = m
	}
	rows := make([]Row, 0, len(pods))
	for _, p := range pods {
		r := Row{Pod: p.Name}
		for _, c := range p.Spec.Containers {
			r.CPURequest += c.Resources.Requests.Cpu().MilliValue()
			r.CPULimit += c.Resources.Limits.Cpu().MilliValue()
			r.MemRequest += c.Resources.Requests.Memory().Value()
			r.MemLimit += c.Resources.Limits.Memory().Value()
		}
		m, ok := byPod[p.Name]
		if !ok {
			r.NoMetrics = true
		}
		for _, c := range m.Containers {
			cpu, err := resource.ParseQuantity(c.Usage.CPU)
			if err != nil {
				return nil, fmt.Errorf("pod %s: bad cpu %q", p.Name, c.Usage.CPU)
			}
			mem, err := resource.ParseQuantity(c.Usage.Memory)
			if err != nil {
				return nil, fmt.Errorf("pod %s: bad memory %q", p.Name, c.Usage.Memory)
			}
			r.CPU += cpu.MilliValue()
			r.Memory += mem.Value()
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Pod < rows[j].Pod })
	return rows, nil
}

func cpu(m int64) string {
	if m == 0 {
		return "-"
	}
	return fmt.Sprintf("%dm", m)
}

func mem(b int64) string {
	if b == 0 {
		return "-"
	}
	return fmt.Sprintf("%dMi", b/(1<<20))
}

// usage renders "used (pct% of limit)"; no percentage without a limit.
func usage(used, limit int64, fmtf func(int64) string) string {
	if limit == 0 || used == 0 {
		return fmtf(used)
	}
	return fmt.Sprintf("%s (%d%%)", fmtf(used), used*100/limit)
}

// Render writes rows as "table" or "json".
func Render(w io.Writer, rows []Row, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	case "table":
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "POD\tCPU (of limit)\tCPU REQ\tCPU LIM\tMEMORY (of limit)\tMEM REQ\tMEM LIM")
		for _, r := range rows {
			c, m := usage(r.CPU, r.CPULimit, cpu), usage(r.Memory, r.MemLimit, mem)
			if r.NoMetrics {
				c, m = "n/a", "n/a"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Pod, c, cpu(r.CPURequest), cpu(r.CPULimit), m, mem(r.MemRequest), mem(r.MemLimit))
		}
		return tw.Flush()
	}
	return fmt.Errorf("unknown output format %q", format)
}
