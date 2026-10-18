// Package debug adds an ephemeral debug container to a running pod, for images
// that have no shell (distroless, scratch).
package debug

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Add appends an ephemeral container running image to the pod and returns its
// name. target (a container of the pod) makes the debug container share its
// process namespace, so you can see and signal its processes. Ephemeral
// containers cannot be removed: this one lives until the pod is replaced.
func Add(ctx context.Context, cs kubernetes.Interface, ns, pod, image, target string) (string, error) {
	p, err := cs.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	name := "kx-debug-" + hex.EncodeToString(b)
	p.Spec.EphemeralContainers = append(p.Spec.EphemeralContainers, corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:            name,
			Image:           image,
			Command:         []string{"sleep", "infinity"}, // the shell comes from exec, like any other container
			Stdin:           true,
			TTY:             true,
			ImagePullPolicy: corev1.PullIfNotPresent,
		},
		TargetContainerName: target,
	})
	if _, err := cs.CoreV1().Pods(ns).UpdateEphemeralContainers(ctx, pod, p, metav1.UpdateOptions{}); err != nil {
		return "", err
	}
	return name, nil
}

// WaitRunning polls until the ephemeral container is running. Image pull
// failures are returned at once instead of after the timeout.
func WaitRunning(ctx context.Context, cs kubernetes.Interface, ns, pod, name string, timeout time.Duration, poll time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		p, err := cs.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
		if err == nil {
			for _, s := range p.Status.EphemeralContainerStatuses {
				if s.Name != name {
					continue
				}
				if s.State.Running != nil {
					return nil
				}
				if t := s.State.Terminated; t != nil {
					return fmt.Errorf("debug container exited (%s, exit %d)", t.Reason, t.ExitCode)
				}
				if w := s.State.Waiting; w != nil {
					switch w.Reason {
					case "ErrImagePull", "ImagePullBackOff", "InvalidImageName", "CreateContainerError", "CreateContainerConfigError":
						return fmt.Errorf("debug container cannot start: %s: %s", w.Reason, w.Message)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("debug container %s did not start within %s", name, timeout)
		case <-time.After(poll):
		}
	}
}
