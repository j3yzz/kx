// Package env computes a pod's effective environment and where each variable
// comes from. Secret-sourced values are masked unless revealed.
package env

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/j3yzz/kx/internal/kube"
	"github.com/j3yzz/kx/internal/secrets"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Var struct {
	Container string
	Name      string
	Value     string // raw; render through Display
	Source    string // "literal", "configmap app/KEY", "secret app/KEY", "field metadata.name", ...
	Secret    bool   // value came from a Secret: masked unless revealed
	Missing   bool   // referenced ConfigMap/Secret/key does not exist
}

// Display is the value to show: masked for Secrets unless reveal.
func (v Var) Display(reveal bool) string {
	switch {
	case v.Missing:
		return "(missing)"
	case v.Secret && !reveal:
		return secrets.Mask
	}
	return v.Value
}

type source struct {
	kind, name string
	data       map[string]string // nil = not found
}

// Collect returns the effective env of every container in pod. envFrom is
// applied in order (later wins), then explicit env entries override it, as
// Kubernetes does.
func Collect(ctx context.Context, cs kubernetes.Interface, pod *corev1.Pod) ([]Var, error) {
	cache := map[string]*source{}
	fetch := func(kind, name string) (*source, error) {
		k := kind + "/" + name
		if s, ok := cache[k]; ok {
			return s, nil
		}
		s := &source{kind: kind, name: name}
		var err error
		switch kind {
		case "configmap":
			var cm *corev1.ConfigMap
			if cm, err = cs.CoreV1().ConfigMaps(pod.Namespace).Get(ctx, name, metav1.GetOptions{}); err == nil {
				s.data = cm.Data
			}
		case "secret":
			var sec *corev1.Secret
			if sec, err = cs.CoreV1().Secrets(pod.Namespace).Get(ctx, name, metav1.GetOptions{}); err == nil {
				s.data = map[string]string{}
				for k, v := range sec.Data {
					s.data[k] = string(v)
				}
			}
		}
		if apierrors.IsNotFound(err) {
			err = nil
		}
		if err != nil {
			return nil, kube.WrapError(fmt.Errorf("get %s %s: %w", kind, name, err))
		}
		cache[k] = s
		return s, nil
	}

	var out []Var
	containers := append(append([]corev1.Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...)
	for _, c := range containers {
		vars := map[string]Var{}
		for _, ef := range c.EnvFrom {
			kind, name := "configmap", ""
			if ef.ConfigMapRef != nil {
				name = ef.ConfigMapRef.Name
			} else if ef.SecretRef != nil {
				kind, name = "secret", ef.SecretRef.Name
			} else {
				continue
			}
			s, err := fetch(kind, name)
			if err != nil {
				return nil, err
			}
			if s.data == nil {
				vars[ef.Prefix+"*"] = Var{Container: c.Name, Name: ef.Prefix + "*", Source: kind + " " + name, Missing: true}
				continue
			}
			for k, v := range s.data {
				vars[ef.Prefix+k] = Var{c.Name, ef.Prefix + k, v, kind + " " + name + "/" + k, kind == "secret", false}
			}
		}
		for _, e := range c.Env {
			v, err := explicit(c.Name, e, fetch)
			if err != nil {
				return nil, err
			}
			vars[e.Name] = v
		}
		names := make([]string, 0, len(vars))
		for n := range vars {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, vars[n])
		}
	}
	return out, nil
}

func explicit(container string, e corev1.EnvVar, fetch func(kind, name string) (*source, error)) (Var, error) {
	v := Var{Container: container, Name: e.Name, Source: "literal", Value: e.Value}
	f := e.ValueFrom
	if f == nil {
		return v, nil
	}
	ref := func(kind, name, key string) (Var, error) {
		v.Source, v.Secret = kind+" "+name+"/"+key, kind == "secret"
		s, err := fetch(kind, name)
		if err != nil {
			return v, err
		}
		val, ok := s.data[key]
		v.Value, v.Missing = val, !ok
		return v, nil
	}
	switch {
	case f.ConfigMapKeyRef != nil:
		return ref("configmap", f.ConfigMapKeyRef.Name, f.ConfigMapKeyRef.Key)
	case f.SecretKeyRef != nil:
		return ref("secret", f.SecretKeyRef.Name, f.SecretKeyRef.Key)
	case f.FieldRef != nil:
		v.Source, v.Value = "field "+f.FieldRef.FieldPath, ""
	case f.ResourceFieldRef != nil:
		v.Source, v.Value = "resource "+f.ResourceFieldRef.Resource, ""
	}
	return v, nil
}

// Render writes vars as "table", "json" or "env".
func Render(w io.Writer, vars []Var, format string, reveal bool) error {
	switch format {
	case "json":
		type row struct {
			Container string `json:"container"`
			Name      string `json:"name"`
			Value     string `json:"value,omitempty"`
			Source    string `json:"source"`
			Secret    bool   `json:"secret,omitempty"`
			Missing   bool   `json:"missing,omitempty"`
		}
		rows := make([]row, len(vars))
		for i, v := range vars {
			rows[i] = row{v.Container, v.Name, v.Display(reveal), v.Source, v.Secret, v.Missing}
			if v.Missing {
				rows[i].Value = ""
			}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	case "env":
		for _, v := range vars {
			if !v.Missing {
				fmt.Fprintf(w, "%s=%s\n", v.Name, secrets.EnvQuote(v.Display(reveal)))
			}
		}
		return nil
	case "table":
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tVALUE\tSOURCE\tCONTAINER")
		for _, v := range vars {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", v.Name, v.Display(reveal), v.Source, v.Container)
		}
		return tw.Flush()
	}
	return fmt.Errorf("unknown output format %q", format)
}
