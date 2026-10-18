package cmd

import (
	"context"
	"os"
	"time"

	"github.com/j3yzz/kx/internal/config"
	"github.com/j3yzz/kx/internal/guard"
	"github.com/j3yzz/kx/internal/kube"
	"github.com/j3yzz/kx/internal/rbac"
)

var cfgCache *config.Config

// loadConfig reads ~/.config/kx/config.yaml once; a missing file is fine.
func loadConfig() (*config.Config, error) {
	if cfgCache != nil {
		return cfgCache, nil
	}
	p, err := config.Path()
	if err != nil {
		return nil, err
	}
	c, err := config.Load(p)
	if err != nil {
		return nil, err
	}
	cfgCache = c
	return c, nil
}

// guardCheck gates an action on the current context (prod confirmation).
func guardCheck(c *kube.Client, a guard.Action, target string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	g := &guard.Guard{
		ProdContexts: cfg.ProdContexts,
		Yes:          Global.Yes,
		Interactive:  kube.IsTTY(os.Stdin),
		In:           os.Stdin,
		Out:          os.Stderr,
	}
	return g.Check(c.Context, c.Namespace, a, target)
}

// Permissions the commands need; checked up front by requireAccess.
var (
	getPods      = rbac.Req{Verb: "get", Resource: "pods"}
	listPods     = rbac.Req{Verb: "list", Resource: "pods"}
	execPods     = rbac.Req{Verb: "create", Resource: "pods", Subresource: "exec"}
	forwardPods  = rbac.Req{Verb: "create", Resource: "pods", Subresource: "portforward"}
	logPods      = rbac.Req{Verb: "get", Resource: "pods", Subresource: "log"}
	getSecrets   = rbac.Req{Verb: "get", Resource: "secrets"}
	getConfigMap = rbac.Req{Verb: "get", Resource: "configmaps"}
)

// requireAccess fails with one readable error if the user lacks a permission
// the command needs. It runs the checks in parallel with a short timeout.
func requireAccess(c *kube.Client, reqs ...rbac.Req) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return rbac.Check(ctx, c, c.Namespace, c.Context, reqs...)
}
