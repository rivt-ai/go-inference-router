package router

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/router/config"
	"github.com/rivt-ai/go-inference-router/router/install"
)

// registryURL and registryPublicKeys are the release trust root. The keys are
// injected into release builds with
// -ldflags "-X github.com/rivt-ai/go-inference-router/router.registryPublicKeys=..."
//
// registryPublicKeys is a comma-separated list rather than a single key so the
// signing key can be rotated: a build trusts both the outgoing and incoming key
// during an overlap window, and the old one is dropped from later builds. A
// binary can only ever trust keys compiled into it, so a single-key trust root
// makes both rotation and compromise recovery impossible for installs already
// in the wild.
//
// They live here rather than in the command so that the shipped binary and an
// embedding host share one trust policy instead of each deriving its own. A
// host that guessed wrong here would silently enable or disable execution of
// unmanaged binaries found on PATH.
var (
	registryURL        = "https://github.com/rivt-ai/go-inference-router/releases/latest/download/providers.json"
	registryPublicKeys string
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

	// Secrets resolves credential references.
	//
	// Deliberately not defaulted: the OS keychain and encrypted store live in
	// router/secret, and importing them costs age, go-keyring, and dbus. A host
	// that wants them opts in with secret.DefaultResolver(), which is also the
	// point at which it accepts those dependencies. Leaving this nil is correct
	// for a configuration with no secrets: reference resolution then fails with
	// a message naming this field.
	Secrets SecretResolver

	// Observer receives lifecycle, request, and install observations.
	Observer llm.Observer

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
	installer, err := NewInstaller(cfg, options.Observer)
	if err != nil {
		return nil, err
	}
	source := DefaultSource{
		Secrets: options.Secrets, Stderr: stderr, Observer: options.Observer,
		AllowPathLookup: PathLookupAllowed(cfg),
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
// binaries found on PATH may be executed.
//
// Lookup is permitted when the operator asks for it, or when no registry trust
// root exists at all — the latter is the development case, where refusing every
// unsigned provider would leave no way to run one.
func PathLookupAllowed(cfg config.Config) bool {
	return cfg.Registry.AllowPathLookup || trustRoot(cfg) == ""
}

// trustRoot returns the configured trust root, preferring an explicit
// configuration override over the compiled-in keys. Overriding replaces the
// release keys outright rather than adding to them, so an operator pointing at
// their own registry does not keep trusting ours as well.
func trustRoot(cfg config.Config) string {
	if configured := cfg.Registry.TrustedKeys(); configured != "" {
		return configured
	}
	return registryPublicKeys
}

// NewInstaller builds the provider installer for a configuration, applying the
// release trust root unless the configuration overrides it. It returns nil when
// no public key is available, which disables managed installation.
func NewInstaller(cfg config.Config, observer llm.Observer) (*install.Installer, error) {
	url := registryURL
	if cfg.Registry.URL != "" {
		url = cfg.Registry.URL
	}
	root := trustRoot(cfg)
	if root == "" {
		return nil, nil
	}
	keys, err := install.ParseTrustedKeys(root)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	return install.New(url, keys, filepath.Join(dir, "go-inference-router", "providers"), nil, observer), nil
}
