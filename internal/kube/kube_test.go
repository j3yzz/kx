package kube

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const kubeconfig = `apiVersion: v1
kind: Config
current-context: stg
clusters:
- name: c
  cluster: {server: "https://127.0.0.1:1"}
users:
- name: u
  user: {token: t}
contexts:
- name: stg
  context: {cluster: c, user: u, namespace: payments-stg}
- name: prod
  context: {cluster: c, user: u}
`

func write(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNew(t *testing.T) {
	kc := write(t)
	cases := []struct {
		name       string
		o          Options
		ctx, ns    string
		nonDefault bool
	}{
		{"defaults", Options{Kubeconfig: kc}, "stg", "payments-stg", false},
		{"ns flag", Options{Kubeconfig: kc, Namespace: "other"}, "stg", "other", true},
		{"context flag", Options{Kubeconfig: kc, Context: "prod"}, "prod", "default", true},
		{"same as default", Options{Kubeconfig: kc, Context: "stg", Namespace: "payments-stg"}, "stg", "payments-stg", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := New(c.o)
			if err != nil {
				t.Fatal(err)
			}
			if got.Context != c.ctx || got.Namespace != c.ns || got.NonDefault != c.nonDefault {
				t.Fatalf("got %s/%s nonDefault=%v", got.Context, got.Namespace, got.NonDefault)
			}
		})
	}
}

func TestAnnounce(t *testing.T) {
	var b bytes.Buffer
	(&Client{Context: "prod", Namespace: "x"}).Announce(&b)
	if b.Len() != 0 {
		t.Fatal("printed while on defaults")
	}
	(&Client{Context: "prod", Namespace: "x", NonDefault: true}).Announce(&b)
	if !strings.Contains(b.String(), "prod") {
		t.Fatalf("got %q", b.String())
	}
}

func TestWrapError(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("nope"))
	if !errors.Is(WrapError(forbidden), ErrDenied) {
		t.Fatal("403 not mapped to ErrDenied")
	}
	other := errors.New("boom")
	if WrapError(other) != other {
		t.Fatal("non-403 changed")
	}
}
