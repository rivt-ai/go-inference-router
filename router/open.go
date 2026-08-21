package router

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/router/config"
	"github.com/rivt-ai/go-inference-router/router/install"
)

// registryURL and registryPublicKey are the release trust root. The public key
// is injected into release builds with
// -ldflags "-X github.com/rivt-ai/go-inference-router/router.registryPublicKey=..."
//
// They live here rather than in the command so that the shipped binary and an
// embedding host share one trust policy instead of each deriving its own. A
// host that guessed wrong here would silently enable or disable execution of
// unmanaged binaries found on PATH.
var (
	registryURL       = "https://github.com/rivt-ai/go-inference-router/releases/latest/download/providers.json"
	registryPublicKey string
)

// Options configures Open.
//
// Every field is optional. The zero value opens the user's configuration with
// the default trust policy, which is what most hosts want.
//
// Discipline for future fields: add one only when a host cannot reach the same
// outcome through New and a Source of its own. Open is the trivial path; New is
// the escape hatch.
type Options struct {
	// Config, when non-nil, is used as-is and no configuration file is read.
	//
	// This is the recommended field for a host that already has its own
	// configuration format. It avoids the YAML dependency, avoids introducing a
	// second configuration file, and avoids a second precedence chain that
	// disagrees with the host's own. It also means no workspace file is ever
	// read, so a hostile repository has no configuration to plant.
	Config *config.Config

	// Loader supplies the configuration, and is called again by Reload.
	// Ignored when Config is set; required when it is not.
	//
	// configfile.Loader reads the usual YAML files. It is a separate package so
	// that this one does not import a YAML parser on behalf of hosts that never
	// read a file. Note that reading a workspace file trusts whatever a cloned
	// repository ships; Config avoids that question entirely.
	Loader func(context.Context) (config.Config, error)

	// Secrets resolves credential references. Nil defaults to EnvResolver,
	// which handles env and file references with the standard library alone.
	//
	// Keychain and encrypted-store references are deliberately not defaulted:
	// they live in router/secret, and importing them costs age, go-keyring,
	// and dbus. A host that wants them opts in with secret.DefaultResolver(),
	// which is also the point at which it accepts those dependencies.
	Secrets SecretResolver

	// RegistryVerifier authenticates the provider registry manifest.
	//
	// Deliberately not defaulted beyond the compiled-in key, for the same
	// reason as Secrets: a keyless implementation costs sigstore-go and its
	// transitive dependencies, which is a larger surface than this module
	// carries. A host that wants it supplies its own Verifier, which is also
	// the point at which it accepts those dependencies and decides it can
	// reach a transparency log at verify time.
	//
	// Leaving this nil keeps the Ed25519 trust root: the release key compiled
	// into the binary, or registry.public_key from configuration.
	RegistryVerifier install.Verifier

	// Observer receives lifecycle, request, and install observations.
	Observer llm.Observer

	// HTTPClient, when non-nil, carries requests for in-process providers
	// (proxies, instrumentation). Provider Processes bring their own
	// transport and are unaffected.
	HTTPClient *http.Client

	// HTTPTimeout bounds a whole in-process request; StallTimeout bounds the
	// wait for the next byte of an in-process streaming response. Zero keeps
	// the provider defaults.
	HTTPTimeout  time.Duration
	StallTimeout time.Duration

	// Stderr receives Provider Process stderr. Defaults to os.Stderr.
	Stderr io.Writer
}

// Open wires configuration, secrets, the provider installer, and the registry
// trust policy with defaults, and returns a ready Router.
//
// New remains available for hosts that supply their own Source.
func Open(ctx context.Context, options Options) (*Router, error) {
	cfg, reload, err := openConfig(ctx, options)
	if err != nil {
		return nil, err
	}
	stderr := options.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	installer, err := NewInstaller(cfg, options.RegistryVerifier, options.Observer)
	if err != nil {
		return nil, err
	}
	secrets := options.Secrets
	if secrets == nil {
		secrets = EnvResolver{}
	}
	source := DefaultSource{
		Secrets: secrets, Stderr: stderr, Observer: options.Observer,
		AllowPathLookup: pathLookupAllowed(cfg, installer),
		HTTPClient:      options.HTTPClient, HTTPTimeout: options.HTTPTimeout, StallTimeout: options.StallTimeout,
	}
	if installer != nil {
		source.Locator = installer
	}
	router, err := New(cfg, source, options.Observer)
	if err != nil {
		return nil, err
	}
	router.installer = installer
	router.reload = reload
	return router, nil
}

func openConfig(ctx context.Context, options Options) (config.Config, func(context.Context) (config.Config, error), error) {
	if options.Config != nil {
		cfg := *options.Config
		if err := cfg.Validate(); err != nil {
			return config.Config{}, nil, err
		}
		return cfg, nil, nil
	}
	if options.Loader == nil {
		return config.Config{}, nil, errors.New("router.Options needs either Config or Loader")
	}
	cfg, err := options.Loader(ctx)
	if err != nil {
		return config.Config{}, nil, err
	}
	return cfg, options.Loader, nil
}

// Installer reports the provider installer, or nil when no registry trust root
// is configured. A build without an injected key and without a configured key
// has no installer.
func (r *Router) Installer() *install.Installer { return r.installer }

// Reload re-reads the configuration Open was given. It fails for a Router that
// was constructed from an in-memory configuration, which has no file to reread.
func (r *Router) Reload(ctx context.Context) (config.Config, error) {
	if r.reload == nil {
		return config.Config{}, errors.New("router was not opened from configuration files")
	}
	return r.reload(ctx)
}

// PathLookupAllowed reports whether unmanaged go-inference-router-provider-*
// binaries found on PATH may be executed, judging by the configured and
// compiled-in registry keys alone.
//
// Lookup is permitted when the operator asks for it, or when no registry trust
// root exists at all — the latter is the development case, where refusing every
// unsigned provider would leave no way to run one.
//
// Open does not use this: a key is one way to establish a trust root, not the
// only one, so Open asks whether an installer was actually built. See
// pathLookupAllowed.
func PathLookupAllowed(cfg config.Config) bool {
	return cfg.Registry.AllowPathLookup || registryPublicKey == "" && cfg.Registry.PublicKey == ""
}

// pathLookupAllowed is the decision Open makes. A trust root exists exactly
// when an installer was built, which is a broader question than "is there a
// public key?" — deriving it from the key would silently re-enable unsigned
// PATH binaries for any host that establishes verification some other way,
// the opposite of what opting in to verification means.
func pathLookupAllowed(cfg config.Config, installer *install.Installer) bool {
	return cfg.Registry.AllowPathLookup || installer == nil
}

// NewInstaller builds the provider installer for a configuration, applying the
// release trust root unless the configuration or verifier overrides it. It
// returns nil when no trust root is available at all, which disables managed
// installation.
//
// A supplied verifier wins over the key-based default: a host that opted in to
// another trust model has made a deliberate decision, and silently preferring a
// compiled-in key over it would defeat that.
func NewInstaller(cfg config.Config, verifier install.Verifier, observer llm.Observer) (*install.Installer, error) {
	url := registryURL
	if cfg.Registry.URL != "" {
		url = cfg.Registry.URL
	}
	if verifier == nil {
		keyed, err := keyVerifier(cfg)
		if err != nil || keyed == nil {
			return nil, err
		}
		verifier = keyed
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	return install.New(url, verifier, filepath.Join(dir, "go-inference-router", "providers"), nil, observer), nil
}

// keyVerifier builds the Ed25519 verifier from the configured or compiled-in
// key, or reports nil when neither exists.
func keyVerifier(cfg config.Config) (install.Verifier, error) {
	publicKey := registryPublicKey
	if cfg.Registry.PublicKey != "" {
		publicKey = cfg.Registry.PublicKey
	}
	if publicKey == "" {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("registry public key must be base64-encoded Ed25519")
	}
	return install.NewKeyVerifier(decoded), nil
}
