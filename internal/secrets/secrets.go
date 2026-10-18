// Package secrets finds the Secrets a pod uses and renders them. Read-only:
// kx never writes Secrets. Values are masked unless the caller reveals them.
package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/j3yzz/kx/internal/kube"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Mask replaces values that were not explicitly revealed. Fixed width so the
// length of a value is not leaked either.
const Mask = "********"

// Ref is one place a pod consumes a Secret. An empty Key means "every key of
// the Secret"; Consumer may then contain "{key}", replaced per key.
type Ref struct {
	Secret    string
	Key       string
	Consumer  string
	Container string
}

// Entry is a resolved Ref. Value is raw; render it only through Render/Display.
type Entry struct {
	Secret    string
	Key       string
	Consumer  string
	Container string
	Value     []byte
	Missing   bool // Secret or key does not exist
}

// Display returns the value to show: masked unless reveal.
func (e Entry) Display(reveal bool) string {
	switch {
	case e.Missing:
		return "(missing)"
	case reveal:
		return string(e.Value)
	}
	return Mask
}

// Walk lists every Secret reference in the pod spec: env secretKeyRef,
// envFrom secretRef, secret and projected-secret volumes, and imagePullSecrets.
func Walk(pod *corev1.Pod) []Ref {
	vols := map[string]corev1.Volume{}
	for _, v := range pod.Spec.Volumes {
		vols[v.Name] = v
	}
	var refs []Ref
	containers := append(append([]corev1.Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...)
	for _, c := range containers {
		for _, e := range c.Env {
			if r := e.ValueFrom; r != nil && r.SecretKeyRef != nil {
				refs = append(refs, Ref{r.SecretKeyRef.Name, r.SecretKeyRef.Key, "env " + e.Name, c.Name})
			}
		}
		for _, e := range c.EnvFrom {
			if e.SecretRef != nil {
				refs = append(refs, Ref{e.SecretRef.Name, "", "env " + e.Prefix + "{key}", c.Name})
			}
		}
		for _, m := range c.VolumeMounts {
			v, ok := vols[m.Name]
			if !ok {
				continue
			}
			if v.Secret != nil {
				refs = append(refs, volumeRefs(c.Name, m.MountPath, v.Secret.SecretName, v.Secret.Items)...)
			}
			if v.Projected != nil {
				for _, s := range v.Projected.Sources {
					if s.Secret != nil {
						refs = append(refs, volumeRefs(c.Name, m.MountPath, s.Secret.Name, s.Secret.Items)...)
					}
				}
			}
		}
	}
	for _, s := range pod.Spec.ImagePullSecrets {
		refs = append(refs, Ref{Secret: s.Name, Consumer: "imagePullSecret"})
	}
	return refs
}

func volumeRefs(container, mount, secret string, items []corev1.KeyToPath) []Ref {
	if len(items) == 0 {
		return []Ref{{secret, "", "mount " + path.Join(mount, "{key}"), container}}
	}
	refs := make([]Ref, len(items))
	for i, it := range items {
		refs[i] = Ref{secret, it.Key, "mount " + path.Join(mount, it.Path), container}
	}
	return refs
}

// Collect fetches the referenced Secrets (read-only) and expands refs into
// entries. A missing Secret or key is marked Missing, not an error.
func Collect(ctx context.Context, cs kubernetes.Interface, ns string, refs []Ref) ([]Entry, error) {
	cache := map[string]*corev1.Secret{} // nil = not found
	var out []Entry
	seen := map[[4]string]bool{}
	add := func(e Entry) {
		k := [4]string{e.Secret, e.Key, e.Consumer, e.Container}
		if !seen[k] {
			seen[k] = true
			out = append(out, e)
		}
	}
	for _, r := range refs {
		s, ok := cache[r.Secret]
		if !ok {
			var err error
			s, err = cs.CoreV1().Secrets(ns).Get(ctx, r.Secret, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				s, err = nil, nil
			}
			if err != nil {
				return nil, kube.WrapError(fmt.Errorf("get secret %s: %w", r.Secret, err))
			}
			cache[r.Secret] = s
		}
		base := Entry{Secret: r.Secret, Key: r.Key, Consumer: r.Consumer, Container: r.Container}
		switch {
		case s == nil:
			base.Missing = true
			base.Consumer = strings.ReplaceAll(base.Consumer, "{key}", "*")
			add(base)
		case r.Key != "":
			v, ok := s.Data[r.Key]
			base.Value, base.Missing = v, !ok
			add(base)
		default:
			for k, v := range s.Data {
				e := base
				e.Key, e.Value = k, v
				e.Consumer = strings.ReplaceAll(r.Consumer, "{key}", k)
				add(e)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Secret != b.Secret {
			return a.Secret < b.Secret
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

// Render writes entries as "table", "json" or "env". Values are masked unless reveal.
func Render(w io.Writer, entries []Entry, format string, reveal bool) error {
	switch format {
	case "json":
		type row struct {
			Secret    string `json:"secret"`
			Key       string `json:"key"`
			Consumer  string `json:"consumer"`
			Container string `json:"container,omitempty"`
			Value     string `json:"value,omitempty"`
			Missing   bool   `json:"missing,omitempty"`
		}
		rows := make([]row, len(entries))
		for i, e := range entries {
			rows[i] = row{e.Secret, e.Key, e.Consumer, e.Container, "", e.Missing}
			if !e.Missing {
				rows[i].Value = e.Display(reveal)
			}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	case "env":
		for _, e := range entries {
			if e.Missing {
				continue
			}
			v := e.Display(reveal)
			if strings.ContainsAny(v, " \t\n\"'#$\\") {
				v = strconv.Quote(v)
			}
			fmt.Fprintf(w, "%s=%s\n", e.Key, v)
		}
		return nil
	case "table":
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "SECRET\tKEY\tCONSUMER\tCONTAINER\tVALUE")
		for _, e := range entries {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.Secret, e.Key, e.Consumer, e.Container, e.Display(reveal))
		}
		return tw.Flush()
	}
	return fmt.Errorf("unknown output format %q", format)
}

// Single returns the one distinct secret/key in entries, for --copy.
// Errors never include values.
func Single(entries []Entry) (Entry, error) {
	var found *Entry
	for i := range entries {
		e := &entries[i]
		if e.Missing {
			continue
		}
		if found != nil && (found.Secret != e.Secret || found.Key != e.Key) {
			return Entry{}, fmt.Errorf("--copy needs exactly one key; narrow with --key (matched %s/%s and %s/%s)", found.Secret, found.Key, e.Secret, e.Key)
		}
		if found == nil {
			found = e
		}
	}
	if found == nil {
		return Entry{}, fmt.Errorf("no matching key to copy")
	}
	return *found, nil
}
