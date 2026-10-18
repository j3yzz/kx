// Package configmaps finds the ConfigMaps a pod consumes, like package secrets
// but without masking: ConfigMap values are configuration, not credentials.
package configmaps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/j3yzz/kx/internal/kube"
	"github.com/j3yzz/kx/internal/secrets"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Ref is one place a pod consumes a ConfigMap. Empty Key = every key; Consumer
// may then contain "{key}".
type Ref struct{ Name, Key, Consumer, Container string }

type Entry struct {
	ConfigMap, Key, Consumer, Container, Value string
	Missing                                    bool
}

// Walk lists ConfigMap references: env configMapKeyRef, envFrom configMapRef,
// configMap volumes and projected configMap sources.
func Walk(pod *corev1.Pod) []Ref {
	vols := map[string]corev1.Volume{}
	for _, v := range pod.Spec.Volumes {
		vols[v.Name] = v
	}
	var refs []Ref
	containers := append(append([]corev1.Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...)
	for _, c := range containers {
		for _, e := range c.Env {
			if r := e.ValueFrom; r != nil && r.ConfigMapKeyRef != nil {
				refs = append(refs, Ref{r.ConfigMapKeyRef.Name, r.ConfigMapKeyRef.Key, "env " + e.Name, c.Name})
			}
		}
		for _, e := range c.EnvFrom {
			if e.ConfigMapRef != nil {
				refs = append(refs, Ref{e.ConfigMapRef.Name, "", "env " + e.Prefix + "{key}", c.Name})
			}
		}
		for _, m := range c.VolumeMounts {
			v, ok := vols[m.Name]
			if !ok {
				continue
			}
			if v.ConfigMap != nil {
				refs = append(refs, volumeRefs(c.Name, m.MountPath, v.ConfigMap.Name, v.ConfigMap.Items)...)
			}
			if v.Projected != nil {
				for _, s := range v.Projected.Sources {
					if s.ConfigMap != nil {
						refs = append(refs, volumeRefs(c.Name, m.MountPath, s.ConfigMap.Name, s.ConfigMap.Items)...)
					}
				}
			}
		}
	}
	return refs
}

func volumeRefs(container, mount, name string, items []corev1.KeyToPath) []Ref {
	if len(items) == 0 {
		return []Ref{{name, "", "mount " + path.Join(mount, "{key}"), container}}
	}
	refs := make([]Ref, len(items))
	for i, it := range items {
		refs[i] = Ref{name, it.Key, "mount " + path.Join(mount, it.Path), container}
	}
	return refs
}

// Collect fetches the referenced ConfigMaps and expands refs into entries.
// A missing ConfigMap or key is marked Missing, not an error.
func Collect(ctx context.Context, cs kubernetes.Interface, ns string, refs []Ref) ([]Entry, error) {
	cache := map[string]*corev1.ConfigMap{} // nil = not found
	var out []Entry
	seen := map[[4]string]bool{}
	add := func(e Entry) {
		k := [4]string{e.ConfigMap, e.Key, e.Consumer, e.Container}
		if !seen[k] {
			seen[k] = true
			out = append(out, e)
		}
	}
	for _, r := range refs {
		cm, ok := cache[r.Name]
		if !ok {
			var err error
			cm, err = cs.CoreV1().ConfigMaps(ns).Get(ctx, r.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				cm, err = nil, nil
			}
			if err != nil {
				return nil, kube.WrapError(fmt.Errorf("get configmap %s: %w", r.Name, err))
			}
			cache[r.Name] = cm
		}
		base := Entry{ConfigMap: r.Name, Key: r.Key, Consumer: r.Consumer, Container: r.Container}
		switch {
		case cm == nil:
			base.Missing, base.Consumer = true, strings.ReplaceAll(base.Consumer, "{key}", "*")
			add(base)
		case r.Key != "":
			v, ok := cm.Data[r.Key]
			base.Value, base.Missing = v, !ok
			add(base)
		default:
			for k, v := range cm.Data {
				e := base
				e.Key, e.Value = k, v
				e.Consumer = strings.ReplaceAll(r.Consumer, "{key}", k)
				add(e)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.ConfigMap != b.ConfigMap {
			return a.ConfigMap < b.ConfigMap
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		return a.Consumer < b.Consumer
	})
	return out, nil
}

// FilterKey keeps entries whose key is key; empty key keeps everything.
func FilterKey(entries []Entry, key string) []Entry {
	if key == "" {
		return entries
	}
	var out []Entry
	for _, e := range entries {
		if e.Key == key {
			out = append(out, e)
		}
	}
	return out
}

func (e Entry) display() string {
	if e.Missing {
		return "(missing)"
	}
	return e.Value
}

// Render writes entries as "table", "json" or "env".
func Render(w io.Writer, entries []Entry, format string) error {
	switch format {
	case "json":
		type row struct {
			ConfigMap string `json:"configmap"`
			Key       string `json:"key"`
			Consumer  string `json:"consumer"`
			Container string `json:"container,omitempty"`
			Value     string `json:"value,omitempty"`
			Missing   bool   `json:"missing,omitempty"`
		}
		rows := make([]row, len(entries))
		for i, e := range entries {
			rows[i] = row{e.ConfigMap, e.Key, e.Consumer, e.Container, e.Value, e.Missing}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	case "env":
		for _, e := range entries {
			if !e.Missing {
				fmt.Fprintf(w, "%s=%s\n", e.Key, secrets.EnvQuote(e.Value))
			}
		}
		return nil
	case "table":
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "CONFIGMAP\tKEY\tCONSUMER\tCONTAINER\tVALUE")
		for _, e := range entries {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.ConfigMap, e.Key, e.Consumer, e.Container, oneLine(e.display()))
		}
		return tw.Flush()
	}
	return fmt.Errorf("unknown output format %q", format)
}

// oneLine keeps multi-line values (whole config files) from breaking the table.
func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}
