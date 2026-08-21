package router

import (
	"fmt"
	"os"
	"strings"

	"github.com/rivt-ai/go-inference-router/router/config"
)

// EnvResolver resolves env and file secret references with the standard
// library alone.
//
// It exists so a host whose credentials live in the environment does not
// import router/secret, which costs age, go-keyring, and dbus. Keychain and
// encrypted-store references fail with an error naming that package as the
// opt-in.
type EnvResolver struct{}

// Resolve returns the secret for env and file references.
func (EnvResolver) Resolve(ref config.SecretRef) (string, error) {
	switch {
	case ref.Env != "":
		value, ok := os.LookupEnv(ref.Env)
		if !ok {
			return "", fmt.Errorf("environment secret %q is not set", ref.Env)
		}
		return value, nil
	case ref.File != "":
		data, err := os.ReadFile(ref.File)
		if err != nil {
			return "", fmt.Errorf("file secret: %w", err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	case ref.Keychain != "":
		return "", fmt.Errorf("keychain secret %q needs a resolver from router/secret; set Options.Secrets", ref.Keychain)
	case ref.Secret != "":
		return "", fmt.Errorf("encrypted-store secret %q needs a resolver from router/secret; set Options.Secrets", ref.Secret)
	default:
		return "", fmt.Errorf("empty secret reference")
	}
}
