// Package config declares Router Model Profiles and Provider Definitions.
//
// It deliberately depends on nothing outside the standard library: a host that
// builds a configuration in memory, or implements its own router.Source, needs
// these types without a YAML parser. Reading configuration from files lives in
// router/configfile.
package config

import (
	"fmt"
	"strings"
)

// Version is the only supported configuration version.
const Version = 1

// Config is the merged Router configuration.
type Config struct {
	Version   int                     `yaml:"version"`
	Registry  Registry                `yaml:"registry,omitempty"`
	Providers map[string]Provider     `yaml:"providers"`
	Models    map[string]ModelProfile `yaml:"models"`
}

// Registry configures a signed Provider Process registry.
type Registry struct {
	URL string `yaml:"url,omitempty"`
	// PublicKey names a single trusted key.
	//
	// Deprecated: use PublicKeys, which can name more than one so a signing
	// key can be rotated with an overlap window. PublicKey is still read, and
	// merges with PublicKeys when both are set.
	PublicKey string `yaml:"public_key,omitempty"`
	// PublicKeys lists every base64 Ed25519 key whose signature is accepted
	// for this registry's manifest.
	PublicKeys      []string `yaml:"public_keys,omitempty"`
	AllowPathLookup bool     `yaml:"allow_path_lookup,omitempty"`
}

// Configured reports whether a registry section sets anything at all, so a
// merge can tell "absent" from "present but empty" without naming each field
// at the call site — and without a new field being silently ignored.
func (r Registry) Configured() bool {
	return r.URL != "" || r.TrustedKeys() != "" || r.AllowPathLookup
}

// Equal reports whether two registry settings are the same. Registry settings
// are bootstrap-only, so Apply compares them to reject a change that would
// need a restart; PublicKeys makes the struct uncomparable, so the check is
// spelled out here rather than with !=.
func (r Registry) Equal(other Registry) bool {
	if r.URL != other.URL || r.PublicKey != other.PublicKey ||
		r.AllowPathLookup != other.AllowPathLookup || len(r.PublicKeys) != len(other.PublicKeys) {
		return false
	}
	for index, key := range r.PublicKeys {
		if key != other.PublicKeys[index] {
			return false
		}
	}
	return true
}

// TrustedKeys returns the configured keys as one comma-separated list, or the
// empty string when the configuration names none. Both the singular and plural
// fields are read so an existing configuration keeps working unchanged.
func (r Registry) TrustedKeys() string {
	keys := make([]string, 0, len(r.PublicKeys)+1)
	if r.PublicKey != "" {
		keys = append(keys, r.PublicKey)
	}
	keys = append(keys, r.PublicKeys...)
	return strings.Join(keys, ",")
}

// Provider is one named Provider Definition.
type Provider struct {
	Type    string               `yaml:"type"`
	Version string               `yaml:"version,omitempty"`
	Path    string               `yaml:"path,omitempty"`
	BaseURL string               `yaml:"base_url,omitempty"`
	Headers map[string]string    `yaml:"headers,omitempty"`
	Options map[string]any       `yaml:"options,omitempty"`
	Secrets map[string]SecretRef `yaml:"secrets,omitempty"`
}

// ModelProfile maps a selectable ID to one provider model.
type ModelProfile struct {
	Provider string         `yaml:"provider"`
	Model    string         `yaml:"model"`
	Options  map[string]any `yaml:"options,omitempty"`
}

// SecretRef accepts one reference form and deliberately rejects scalar YAML.
type SecretRef struct {
	Env      string `yaml:"env,omitempty"`
	Keychain string `yaml:"keychain,omitempty"`
	Secret   string `yaml:"secret,omitempty"`
	File     string `yaml:"file,omitempty"`
}

func (s SecretRef) count() int {
	count := 0
	for _, value := range []string{s.Env, s.Keychain, s.Secret, s.File} {
		if value != "" {
			count++
		}
	}
	return count
}

func (c Config) validate() error {
	if c.Version != Version {
		return fmt.Errorf("config version %d is unsupported; want %d", c.Version, Version)
	}
	for id, provider := range c.Providers {
		if err := provider.validate(id); err != nil {
			return err
		}
	}
	for id, profile := range c.Models {
		if profile.Provider == "" || profile.Model == "" {
			return fmt.Errorf("model profile %q requires provider and model", id)
		}
		if _, ok := c.Providers[profile.Provider]; !ok {
			return fmt.Errorf("model profile %q references unknown provider %q", id, profile.Provider)
		}
	}
	return nil
}

// Validate checks a programmatically constructed Router configuration.
func (c Config) Validate() error { return c.validate() }

func (p Provider) validate(id string) error {
	if id == "" || p.Type == "" {
		return fmt.Errorf("provider %q has no type", id)
	}
	for key := range p.Options {
		if reservedProviderKey(key) {
			return fmt.Errorf("provider %q option %q conflicts with a common field", id, key)
		}
	}
	for name, ref := range p.Secrets {
		if name == "" || ref.count() != 1 {
			return fmt.Errorf("provider %q secret %q must set exactly one reference", id, name)
		}
	}
	return nil
}

func reservedProviderKey(key string) bool {
	switch key {
	case "type", "version", "path", "base_url", "headers", "options", "secrets":
		return true
	default:
		return false
	}
}
