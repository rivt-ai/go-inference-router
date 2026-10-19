// Command modsum prints the go.sum lines a module would have at a version,
// computed from a git revision instead of the module proxy.
//
// The root module's zip is built from git the same way the proxy builds it
// (nested modules excluded), so its hash is known before the tag exists. That
// lets a release commit pin each submodule's go.sum to the root version it is
// about to publish, and verify against the proxy once the tag is up.
//
// Usage: go run . <repo dir> <module path> <version> <revision>
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	"golang.org/x/mod/zip"
)

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: modsum <repo dir> <module path> <version> <revision>")
		os.Exit(2)
	}
	if err := run(os.Stdout, os.Args[1], os.Args[2], os.Args[3], os.Args[4]); err != nil {
		fmt.Fprintln(os.Stderr, "modsum:", err)
		os.Exit(1)
	}
}

func run(out io.Writer, dir, path, version, revision string) error {
	tmp, err := os.CreateTemp("", "modsum-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	// git archive rather than zip.CreateFromVCS, which needs a .git directory
	// and so fails in a worktree; the extracted tree zips to the same hash.
	src, err := os.MkdirTemp("", "modsum-src-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(src)
	archive := exec.Command("sh", "-c", `git -C "$1" archive "$2" | tar -x -C "$3"`, "sh", dir, revision, src)
	if msg, err := archive.CombinedOutput(); err != nil {
		return fmt.Errorf("git archive %s: %w: %s", revision, err, msg)
	}
	if err := zip.CreateFromDir(tmp, module.Version{Path: path, Version: version}, src); err != nil {
		return fmt.Errorf("zip %s: %w", revision, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	zipHash, err := dirhash.HashZip(tmp.Name(), dirhash.Hash1)
	if err != nil {
		return err
	}
	gomod, err := exec.Command("git", "-C", dir, "show", revision+":go.mod").Output()
	if err != nil {
		return fmt.Errorf("read go.mod at %s: %w", revision, err)
	}
	modHash, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(gomod)), nil
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s %s %s\n%s %s/go.mod %s\n", path, version, zipHash, path, version, modHash)
	return err
}
