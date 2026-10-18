package rbac

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/j3yzz/kx/internal/kube"
	authv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func reviewer(allow func(authv1.ResourceAttributes) bool, fail bool) *fake.Clientset {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "selfsubjectaccessreviews", func(a ktesting.Action) (bool, runtime.Object, error) {
		if fail {
			return true, nil, errors.New("review not permitted")
		}
		r := a.(ktesting.CreateAction).GetObject().(*authv1.SelfSubjectAccessReview)
		r.Status.Allowed = allow(*r.Spec.ResourceAttributes)
		return true, r, nil
	})
	return cs
}

func TestCheck(t *testing.T) {
	get := Req{Verb: "get", Resource: "pods"}
	exec := Req{Verb: "create", Resource: "pods", Subresource: "exec"}
	secrets := Req{Verb: "get", Resource: "secrets"}

	all := reviewer(func(authv1.ResourceAttributes) bool { return true }, false)
	if err := Check(context.Background(), all, "n", "ctx", get, exec); err != nil {
		t.Fatalf("all allowed: %v", err)
	}

	noExecNoSecrets := reviewer(func(a authv1.ResourceAttributes) bool {
		return a.Subresource != "exec" && a.Resource != "secrets"
	}, false)
	err := Check(context.Background(), noExecNoSecrets, "prod", "dataak", get, exec, secrets)
	if !errors.Is(err, kube.ErrDenied) {
		t.Fatalf("want ErrDenied, got %v", err)
	}
	for _, want := range []string{"create pods/exec", "get secrets", "namespace prod", "context dataak"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "get pods") {
		t.Errorf("allowed permission listed as missing: %v", err)
	}

	// A review that cannot be done must not block the command.
	if err := Check(context.Background(), reviewer(nil, true), "n", "ctx", get); err != nil {
		t.Fatalf("review failure should not deny: %v", err)
	}
}
