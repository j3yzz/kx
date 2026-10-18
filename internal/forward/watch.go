package forward

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

// podGone blocks until the pod is deleted, terminating or finished (true), or
// ctx ends (false). The tunnel itself cannot be trusted to report this: it runs
// through the kubelet, whose connection can outlive the pod's containers, so a
// deleted pod leaves the tunnel "open" but dead.
func podGone(ctx context.Context, cs kubernetes.Interface, ns, name string) bool {
	for ctx.Err() == nil {
		w, err := cs.CoreV1().Pods(ns).Watch(ctx, metav1.ListOptions{FieldSelector: "metadata.name=" + name})
		if err != nil {
			select { // transient API error: retry
			case <-ctx.Done():
				return false
			case <-time.After(time.Second):
				continue
			}
		}
		gone, ended := consume(ctx, w, name)
		w.Stop()
		if gone {
			return true
		}
		if !ended { // ctx done
			return false
		}
		// watch closed by the server (normal timeout): re-establish
	}
	return false
}

// consume reads events until the pod is gone (gone=true), the watch closes
// (ended=true) or ctx is done.
func consume(ctx context.Context, w watch.Interface, name string) (gone, ended bool) {
	for {
		select {
		case <-ctx.Done():
			return false, false
		case ev, ok := <-w.ResultChan():
			if !ok {
				return false, true
			}
			p, ok := ev.Object.(*corev1.Pod)
			if !ok || p.Name != name {
				continue
			}
			if ev.Type == watch.Deleted || p.DeletionTimestamp != nil ||
				p.Status.Phase == corev1.PodFailed || p.Status.Phase == corev1.PodSucceeded {
				return true, true
			}
		}
	}
}
