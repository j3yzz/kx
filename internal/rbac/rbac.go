// Package rbac checks, before a command runs, that the user may do what it
// needs, so a missing permission is one readable error instead of a 403 from
// halfway through.
package rbac

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/j3yzz/kx/internal/kube"
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Req is one permission a command needs.
type Req struct{ Verb, Group, Resource, Subresource string }

func (r Req) String() string {
	s := r.Verb + " " + r.Resource
	if r.Subresource != "" {
		s += "/" + r.Subresource
	}
	return s
}

// Check returns nil if every req is allowed in ns. If the access review
// itself cannot be done (API error, review not permitted), that req is
// assumed allowed: the real call will still fail with the real error.
func Check(ctx context.Context, cs kubernetes.Interface, ns, kubeContext string, reqs ...Req) error {
	denied := make([]bool, len(reqs))
	var wg sync.WaitGroup
	for i, r := range reqs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authv1.SelfSubjectAccessReview{
				Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authv1.ResourceAttributes{
					Namespace: ns, Verb: r.Verb, Group: r.Group, Resource: r.Resource, Subresource: r.Subresource,
				}},
			}, metav1.CreateOptions{})
			denied[i] = err == nil && !resp.Status.Allowed
		}()
	}
	wg.Wait()

	var missing []string
	for i, r := range reqs {
		if denied[i] {
			missing = append(missing, r.String())
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%w: your user cannot %s in namespace %s (context %s); ask a cluster admin for this permission, or check --context/-n",
		kube.ErrDenied, strings.Join(missing, ", "), ns, kubeContext)
}
