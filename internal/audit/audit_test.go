package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAppend(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "audit.log")
	for _, target := range []string{"a", "b"} {
		if err := Append(p, Entry{Context: "prod", Namespace: "n", Action: "exec", Target: target, Outcome: "allowed"}); err != nil {
			t.Fatal(err)
		}
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	f, _ := os.Open(p)
	var got []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		got = append(got, e)
	}
	if len(got) != 2 || got[1].Target != "b" || got[0].Time.IsZero() {
		t.Fatalf("got %+v", got)
	}
}
