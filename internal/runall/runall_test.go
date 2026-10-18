package runall

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestDo(t *testing.T) {
	jobs := []Job{
		{"a", func(_ context.Context, o, _ io.Writer) error {
			o.Write([]byte("one\ntw"))
			o.Write([]byte("o"))
			return nil
		}},
		{"b", func(_ context.Context, _, e io.Writer) error { e.Write([]byte("bad\n")); return errors.New("exit 3") }},
	}
	var out, errOut bytes.Buffer
	res := Do(context.Background(), jobs, &out, &errOut)
	if out.String() != "a | one\na | two\n" {
		t.Fatalf("out %q", out.String())
	}
	if errOut.String() != "b | bad\n" {
		t.Fatalf("err %q", errOut.String())
	}
	if res[0].Err != nil || res[1].Err == nil || !strings.Contains(res[1].Err.Error(), "3") {
		t.Fatalf("results %+v", res)
	}
}
