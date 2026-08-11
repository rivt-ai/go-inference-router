//go:build linux

package secret

import (
	"errors"
	"net"
	"os/exec"

	"github.com/godbus/dbus/v5"
	keyring "github.com/zalando/go-keyring"
)

func keychainUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, keyring.ErrUnsupportedPlatform) {
		return true
	}
	var networkError *net.OpError
	if errors.As(err, &networkError) {
		return true
	}
	var commandError *exec.Error
	if errors.As(err, &commandError) {
		return true
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return true
	}
	var busError dbus.Error
	if !errors.As(err, &busError) {
		return false
	}
	switch busError.Name {
	case "org.freedesktop.DBus.Error.Disconnected",
		"org.freedesktop.DBus.Error.FileNotFound",
		"org.freedesktop.DBus.Error.NoServer",
		"org.freedesktop.DBus.Error.ServiceUnknown":
		return true
	default:
		return false
	}
}
