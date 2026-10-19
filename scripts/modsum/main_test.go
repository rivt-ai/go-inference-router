package main

import (
	"os/exec"
	"strings"
	"testing"
)

// v0.8.0's hashes as the proxy recorded them (debe109 put them in go.sum).
func TestMatchesProxyHashForV080(t *testing.T) {
	if exec.Command("git", "-C", "../..", "rev-parse", "--verify", "-q", "v0.8.0^{commit}").Run() != nil {
		t.Skip("v0.8.0 tag not in this clone (shallow checkout)")
	}
	var out strings.Builder
	if err := run(&out, "../..", "github.com/rivt-ai/go-inference-router", "v0.8.0", "v0.8.0"); err != nil {
		t.Fatal(err)
	}
	want := "github.com/rivt-ai/go-inference-router v0.8.0 h1:XcgISsP1WQYJ3MS3Qz2fruqQcIhWvzv55FPlKRAU944=\n" +
		"github.com/rivt-ai/go-inference-router v0.8.0/go.mod h1:VXocR4HmKnKa0XF7udkjkcbDs+0Vf/+Wc2blPUNBCoY=\n"
	if out.String() != want {
		t.Fatalf("got\n%swant\n%s", out.String(), want)
	}
}
