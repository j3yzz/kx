// Package cp copies files between the local disk and a pod by streaming tar
// through the exec API, so it needs only `tar` in the container (like kubectl cp).
package cp

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Runner runs a command in the pod with the given streams.
type Runner func(ctx context.Context, command []string, in io.Reader, out, errOut io.Writer) error

// Download copies remote (file or dir) from the pod to local. If local is an
// existing directory the copy lands inside it; otherwise local is the new name.
func Download(ctx context.Context, run Runner, remote, local string) error {
	dir, base := path.Split(path.Clean(remote))
	if dir == "" {
		dir = "."
	}
	root, name := local, path.Base(remote)
	if fi, err := os.Stat(local); err != nil || !fi.IsDir() {
		root, name = filepath.Dir(local), filepath.Base(local)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	pr, pw := io.Pipe()
	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		err := run(ctx, []string{"tar", "cf", "-", "-C", dir, base}, nil, pw, &stderr)
		pw.CloseWithError(err)
		done <- err
	}()
	xerr := Extract(pr, root, base, name)
	pr.Close()
	if err := <-done; err != nil {
		return remoteErr(err, &stderr)
	}
	return xerr
}

// Extract unpacks a tar stream into root, renaming the top-level entry from
// oldName to newName. Entries that would land outside root are refused.
func Extract(r io.Reader, root, oldName, newName string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel := path.Clean(strings.TrimPrefix(h.Name, "/"))
		if rel == oldName || strings.HasPrefix(rel, oldName+"/") {
			rel = newName + strings.TrimPrefix(rel, oldName)
		}
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return fmt.Errorf("refusing tar entry %q outside destination", h.Name)
		}
		dst := filepath.Join(root, filepath.FromSlash(rel))
		switch h.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(dst, 0o755)
		case tar.TypeReg:
			err = writeFile(dst, tr, h.FileInfo().Mode().Perm())
		case tar.TypeSymlink:
			if !inside(root, filepath.Join(filepath.Dir(dst), h.Linkname)) || filepath.IsAbs(h.Linkname) {
				return fmt.Errorf("refusing symlink %q -> %q pointing outside destination", h.Name, h.Linkname)
			}
			os.Remove(dst)
			err = os.Symlink(h.Linkname, dst)
		}
		if err != nil {
			return err
		}
	}
}

func inside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func writeFile(dst string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Upload copies local (file or dir) into the pod at remote. If remote is an
// existing directory the copy lands inside it; otherwise remote is the new name.
func Upload(ctx context.Context, run Runner, local, remote string) error {
	if _, err := os.Lstat(local); err != nil {
		return err
	}
	var stderr bytes.Buffer
	dir, name := path.Dir(path.Clean(remote)), path.Base(remote)
	if run(ctx, []string{"sh", "-c", `test -d "$1"`, "sh", remote}, nil, io.Discard, io.Discard) == nil {
		dir, name = path.Clean(remote), filepath.Base(local)
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(Pack(pw, local, name)) }()
	err := run(ctx, []string{"tar", "xf", "-", "-C", dir}, pr, io.Discard, &stderr)
	pr.CloseWithError(err)
	if err != nil {
		return remoteErr(err, &stderr)
	}
	return nil
}

// Pack writes local as a tar stream whose top-level entry is called name.
func Pack(w io.Writer, local, name string) error {
	tw := tar.NewWriter(w)
	local = filepath.Clean(local)
	err := filepath.WalkDir(local, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if fi.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		h, err := tar.FileInfoHeader(fi, link)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(local, p)
		h.Name = path.Join(name, filepath.ToSlash(rel))
		if fi.IsDir() {
			h.Name += "/"
		} else if !fi.Mode().IsRegular() && link == "" {
			return nil // sockets, devices, pipes
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

func remoteErr(err error, stderr *bytes.Buffer) error {
	msg := strings.TrimSpace(stderr.String())
	if strings.Contains(msg, "not found") || strings.Contains(err.Error(), "executable file not found") {
		return errors.New("the container has no tar; kx cp needs it (try `kx debug` for shell-less images)")
	}
	if msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}
