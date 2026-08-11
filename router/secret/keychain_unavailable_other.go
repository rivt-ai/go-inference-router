//go:build !linux

package secret

import (
	"errors"

	keyring "github.com/zalando/go-keyring"
)

func keychainUnavailable(err error) bool {
	return errors.Is(err, keyring.ErrUnsupportedPlatform)
}
