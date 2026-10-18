// Package kube builds the client-go clientset from the user's kubeconfig.
package kube

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ErrDenied marks an access-denied (403) failure; cmd maps it to its exit code.
var ErrDenied = errors.New("access denied")

// Options are the global kubectl-style flags.
type Options struct {
	Kubeconfig string
	Context    string
	Namespace  string
}

// Client is a clientset plus the context/namespace kx is acting on.
type Client struct {
	kubernetes.Interface
	Config    *rest.Config
	Context   string
	Namespace string
	// NonDefault is true when the flags moved kx away from the kubeconfig's
	// current-context or that context's namespace.
	NonDefault bool
}

// New loads the kubeconfig (honoring KUBECONFIG) and applies the overrides.
func New(o Options) (*Client, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = o.Kubeconfig
	overrides := &clientcmd.ConfigOverrides{CurrentContext: o.Context}
	overrides.Context.Namespace = o.Namespace
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)

	raw, err := cc.RawConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	ns, _, err := cc.Namespace()
	if err != nil {
		return nil, err
	}
	// Namespace/context the user would get with no flags at all.
	defaultNS, _, _ := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).Namespace()

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	ctxName := raw.CurrentContext
	if o.Context != "" {
		ctxName = o.Context
	}
	return &Client{
		Interface:  cs,
		Config:     cfg,
		Context:    ctxName,
		Namespace:  ns,
		NonDefault: ctxName != raw.CurrentContext || ns != defaultNS,
	}, nil
}

// Announce prints "-> context/namespace" to w when kx is off the defaults.
// Use stderr so piped stdout stays machine-parseable.
func (c *Client) Announce(w io.Writer) {
	if c.NonDefault {
		fmt.Fprintf(w, "-> context %s, namespace %s\n", c.Context, c.Namespace)
	}
}

// WrapError turns a 403 into an actionable error wrapping ErrDenied; others pass through.
func WrapError(err error) error {
	if err != nil && apierrors.IsForbidden(err) {
		return fmt.Errorf("%w: %s\nYour kubeconfig user lacks permission for this; check --context/-n or ask a cluster admin", ErrDenied, err)
	}
	return err
}

// IsTTY reports whether f is a terminal (colors/human output only then).
func IsTTY(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }
