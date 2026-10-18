package env

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const sec = "s3cr3t-value"

func vars(t *testing.T) []Var {
	cs := fake.NewSimpleClientset(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "cfg"}, Data: map[string]string{"LEVEL": "debug", "REGION": "eu"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "db"}, Data: map[string][]byte{"password": []byte(sec), "USER": []byte("admin")}},
	)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "n", Name: "p"}, Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Name: "app",
		EnvFrom: []corev1.EnvFromSource{
			{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"}}},
			{Prefix: "DB_", SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "db"}}},
			{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "gone"}}},
		},
		Env: []corev1.EnvVar{
			{Name: "REGION", Value: "us"}, // explicit overrides envFrom
			{Name: "PW", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "db"}, Key: "password"}}},
			{Name: "NOKEY", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"}, Key: "zzz"}}},
			{Name: "POD", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
		},
	}}}}
	v, err := Collect(context.Background(), cs, pod)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func get(vs []Var, name string) Var {
	for _, v := range vs {
		if v.Name == name {
			return v
		}
	}
	return Var{Name: "<absent>"}
}

func TestCollectSourcesAndPrecedence(t *testing.T) {
	vs := vars(t)
	cases := map[string][2]string{ // name -> value, source
		"LEVEL":       {"debug", "configmap cfg/LEVEL"},
		"REGION":      {"us", "literal"}, // overridden
		"DB_password": {sec, "secret db/password"},
		"PW":          {sec, "secret db/password"},
		"POD":         {"", "field metadata.name"},
	}
	for name, want := range cases {
		if v := get(vs, name); v.Value != want[0] || v.Source != want[1] {
			t.Errorf("%s: got %q from %q, want %q from %q", name, v.Value, v.Source, want[0], want[1])
		}
	}
	if v := get(vs, "NOKEY"); !v.Missing {
		t.Error("absent configmap key should be missing")
	}
	if v := get(vs, "*"); !v.Missing || v.Source != "configmap gone" {
		t.Errorf("absent envFrom configmap: %+v", v)
	}
}

func TestSecretsMaskedUnlessRevealed(t *testing.T) {
	vs := vars(t)
	for _, f := range []string{"table", "json", "env"} {
		var b bytes.Buffer
		if err := Render(&b, vs, f, false); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(b.String(), sec) {
			t.Errorf("%s leaked a Secret value:\n%s", f, b.String())
		}
		if !strings.Contains(b.String(), "debug") { // ConfigMap values are not secret
			t.Errorf("%s hid a ConfigMap value", f)
		}
		b.Reset()
		Render(&b, vs, f, true)
		if !strings.Contains(b.String(), sec) {
			t.Errorf("%s --reveal missing value", f)
		}
	}
}
