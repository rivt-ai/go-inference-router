// Package configfile reads Router configuration from YAML files.
//
// It is separate from router/config so that the configuration types stay free
// of a YAML dependency. A host that implements its own router.Source, or builds
// a config.Config programmatically and passes it to router.Options.Config, needs
// the types but not the parser, and should not pay for one it never calls.
package configfile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v4"

	"github.com/rivt-ai/go-inference-router/router/config"
)

// Load reads either explicit alone or user followed by workspace. Definitions
// with the same ID are replaced whole, preventing credentials from one scope
// from leaking into another scope's partial definition.
func Load(userPath, workspacePath, explicit string) (config.Config, error) { //nolint:gocognit // precedence and validation stay visible together
	if explicit != "" {
		cfg, err := read(explicit, false)
		if err != nil {
			return config.Config{}, err
		}
		return cfg, cfg.Validate()
	}
	merged := config.Config{
		Version:   config.Version,
		Providers: map[string]config.Provider{},
		Models:    map[string]config.ModelProfile{},
	}
	for _, path := range []string{userPath, workspacePath} {
		if path == "" {
			continue
		}
		cfg, err := read(path, true)
		if err != nil {
			return config.Config{}, err
		}
		if cfg.Version == 0 {
			continue
		}
		if cfg.Registry.Configured() {
			merged.Registry = cfg.Registry
		}
		for id, provider := range cfg.Providers {
			merged.Providers[id] = provider
		}
		for id, profile := range cfg.Models {
			merged.Models[id] = profile
		}
	}
	return merged, merged.Validate()
}

// DefaultPaths returns the OS user and workspace configuration paths.
func DefaultPaths(workspace string) (string, string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", "", err
	}
	user := filepath.Join(dir, "go-inference-router", "models.yaml")
	if workspace == "" {
		return user, "", nil
	}
	return user, filepath.Join(workspace, ".go-inference-router", "models.yaml"), nil
}

func read(path string, optional bool) (config.Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the caller chooses the configuration path
	if err != nil {
		if optional && errors.Is(err, os.ErrNotExist) {
			return config.Config{}, nil
		}
		return config.Config{}, fmt.Errorf("open config %s: %w", path, err)
	}
	var cfg config.Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		if plaintext := plaintextSecret(data); plaintext != "" {
			return config.Config{}, fmt.Errorf(
				"decode config %s: provider %q secret must be a reference mapping, not plaintext", path, plaintext)
		}
		return config.Config{}, fmt.Errorf("decode config %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return config.Config{}, fmt.Errorf("decode config %s: multiple YAML documents are not allowed", path)
	}
	return cfg, nil
}

// plaintextSecret names the first provider whose secrets map holds a scalar
// rather than a reference mapping, so the decoder's generic type error can be
// replaced with one that says what is actually wrong. Reporting a secret as
// plaintext must never echo its value.
func plaintextSecret(data []byte) string {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil || len(document.Content) == 0 {
		return ""
	}
	providers := mappingValue(document.Content[0], "providers")
	if providers == nil {
		return ""
	}
	for i := 0; i+1 < len(providers.Content); i += 2 {
		secrets := mappingValue(providers.Content[i+1], "secrets")
		if secrets == nil {
			continue
		}
		for j := 1; j < len(secrets.Content); j += 2 {
			if secrets.Content[j].Kind != yaml.MappingNode {
				return providers.Content[i].Value
			}
		}
	}
	return ""
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key && node.Content[i+1].Kind == yaml.MappingNode {
			return node.Content[i+1]
		}
	}
	return nil
}

// Loader returns a configuration loader for router.Options.Loader, reading the
// user file plus an optional workspace file, or explicit alone when set.
func Loader(workspace, explicit string) func(context.Context) (config.Config, error) {
	return func(context.Context) (config.Config, error) {
		userPath, workspacePath, err := DefaultPaths(workspace)
		if err != nil {
			return config.Config{}, err
		}
		return Load(userPath, workspacePath, explicit)
	}
}
