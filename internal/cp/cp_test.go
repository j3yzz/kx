package cp

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackExtractRoundTrip(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "sub", "a.txt"), []byte("hi"), 0o600)
	os.Symlink("sub/a.txt", filepath.Join(src, "ln"))

	var buf bytes.Buffer
	if err := Pack(&buf, src, "renamed"); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := Extract(&buf, dst, "renamed", "out"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "out", "sub", "a.txt"))
	if err != nil || string(b) != "hi" {
		t.Fatalf("got %q %v", b, err)
	}
	if l, _ := os.Readlink(filepath.Join(dst, "out", "ln")); l != "sub/a.txt" {
		t.Fatalf("symlink %q", l)
	}
}

func TestExtractRefusesEscapes(t *testing.T) {
	mk := func(h tar.Header) io.Reader {
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		tw.WriteHeader(&h)
		tw.Close()
		return &b
	}
	bad := []tar.Header{
		{Name: "x/../../evil", Typeflag: tar.TypeReg},
		{Name: "x/ln", Typeflag: tar.TypeSymlink, Linkname: "../../etc/passwd"},
		{Name: "x/ln", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
	}
	for _, h := range bad {
		if err := Extract(mk(h), t.TempDir(), "x", "x"); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("%+v: %v", h, err)
		}
	}
}

func TestDownloadUsesRunner(t *testing.T) {
	var got []string
	run := func(_ context.Context, cmd []string, _ io.Reader, out, _ io.Writer) error {
		got = cmd
		tw := tar.NewWriter(out)
		tw.WriteHeader(&tar.Header{Name: "f.txt", Size: 2, Mode: 0o644})
		tw.Write([]byte("ok"))
		return tw.Close()
	}
	dst := filepath.Join(t.TempDir(), "copy.txt")
	if err := Download(context.Background(), run, "/app/f.txt", dst); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "tar cf - -C /app/ f.txt" {
		t.Fatalf("cmd %v", got)
	}
	if b, _ := os.ReadFile(dst); string(b) != "ok" {
		t.Fatalf("got %q", b)
	}
}
