package cmd

import (
	"os"

	"github.com/j3yzz/kx/internal/config"
	"github.com/j3yzz/kx/internal/guard"
	"github.com/j3yzz/kx/internal/kube"
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
