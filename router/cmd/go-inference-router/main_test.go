package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSecretCommandsFallBackWithoutLinuxSessionBus(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux Secret Service regression")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(dir, "missing-bus"))
	t.Setenv("INFROUTER_SECRET_STORE_KEY", "")
	if err := runSecret([]string{"set", "token"}, strings.NewReader("value"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runSecret([]string{"list"}, strings.NewReader(""), &output); err != nil || output.String() != "token\n" {
		t.Fatalf("list = %q, %v", output.String(), err)
	}
	info, err := os.Stat(filepath.Join(dir, "go-inference-router", "secrets.key"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("identity permissions = %v, %v", info.Mode().Perm(), err)
	}
}
