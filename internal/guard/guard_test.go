package guard

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func g(in string, interactive, yes bool) (*Guard, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return &Guard{ProdContexts: []string{"prod", "*-prod"}, Yes: yes, Interactive: interactive,
		In: strings.NewReader(in), Out: out}, out
}

func TestIsProd(t *testing.T) {
	gd, _ := g("", true, false)
	for ctx, want := range map[string]bool{"prod": true, "eu-prod": true, "staging": false, "production": false, "": false} {
		if gd.IsProd(ctx) != want {
			t.Errorf("%q: want %v", ctx, want)
		}
	}
}

func TestCheck(t *testing.T) {
	// non-prod: never asks
	gd, out := g("", false, false)
	if err := gd.Check("staging", "ns", Exec, "app"); err != nil || out.Len() != 0 {
		t.Fatalf("non-prod: %v %q", err, out)
	}
	// prod + --yes: proceeds silently, even non-interactive
	gd, out = g("", false, true)
	if err := gd.Check("prod", "ns", Exec, "app"); err != nil || out.Len() != 0 {
		t.Fatalf("--yes: %v", err)
	}
	// prod, non-interactive, no --yes: refuses and says how
	gd, _ = g("y\n", false, false)
	if err := gd.Check("prod", "ns", Reveal, "app"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("want --yes hint, got %v", err)
	}
	// prod, interactive
	for in, ok := range map[string]bool{"y\n": true, "YES\n": true, "n\n": false, "\n": false, "": false, "maybe\n": false} {
		gd, out = g(in, true, false)
		err := gd.Check("prod", "ns", Forward, "mysql")
		if ok != (err == nil) || (!ok && !errors.Is(err, ErrDeclined)) {
			t.Errorf("answer %q: err %v", in, err)
		}
		if !strings.Contains(out.String(), `"prod"`) || !strings.Contains(out.String(), "mysql") {
			t.Errorf("prompt lacks context/target: %q", out)
		}
	}
}
