package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
	"github.com/rivt-ai/go-inference-router/provider/openaicompat"
	"github.com/rivt-ai/go-inference-router/router/config"
	"github.com/rivt-ai/go-inference-router/router/providerproc"
)

// ProviderLocator finds managed Provider Process installations.
type ProviderLocator interface {
	Locate(providerType, version string) (string, bool, error)
}

// SecretResolver turns a credential reference into its value.
//
// It is an interface rather than router/secret.Resolver so that this package
// does not import age, go-keyring, and dbus on behalf of hosts that resolve
// credentials some other way. secret.Resolver satisfies it.
type SecretResolver interface {
	Resolve(ref config.SecretRef) (string, error)
}

// DefaultSource opens the dependency-free compatible provider in-process and
// all SDK-backed providers as isolated processes.
type DefaultSource struct {
	Secrets  SecretResolver
	Locator  ProviderLocator
	Stderr   io.Writer
	Observer llm.Observer
	// AllowPathLookup permits unmanaged go-inference-router-provider-* binaries from PATH.
	AllowPathLookup bool

	startProcess func(context.Context, providerproc.Options) (llm.Provider, error)
}

// Available reports whether a Provider Definition can be opened locally.
func (s DefaultSource) Available(ctx context.Context, id string, definition config.Provider) bool {
	if definition.Type == "openai-compatible" {
		return true
	}
	_, ok, err := s.path(ctx, id, definition)
	return ok && err == nil
}

// Open constructs the built-in adapter or starts an isolated Provider Process.
func (s DefaultSource) Open(ctx context.Context, id string, definition config.Provider) (llm.Provider, error) {
	secrets, err := s.providerSecrets(definition)
	if err != nil {
		return nil, &llm.Error{Kind: llm.KindAuth, Provider: id, Message: err.Error(), Err: err}
	}
	if definition.Type == "openai-compatible" {
		return openaicompat.New(openaicompat.Config{
			Name: id, BaseURL: definition.BaseURL, APIKey: secrets["api_key"], Headers: definition.Headers,
			MetadataPath: stringOption(definition.Options, "metadata_path"),
		}), nil
	}
	path, ok, err := s.path(ctx, id, definition)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &llm.Error{Kind: llm.KindUnavailable, Provider: id, Message: "provider process is not installed"}
	}
	configJSON, err := providerConfig(definition)
	if err != nil {
		return nil, err
	}
	return s.start(ctx, providerproc.Options{
		Path: path, Env: providerEnvironment(), Stderr: s.Stderr, Observer: s.Observer,
		Initialize: llmv1.ProviderInitializeRequest{
			ProviderID: id, Protocols: []string{llmv1.Protocol}, Config: configJSON, Secrets: secrets,
			Observations: s.Observer != nil,
		},
	})
}

func (s DefaultSource) providerSecrets(definition config.Provider) (map[string]string, error) {
	values := map[string]string{}
	if len(definition.Secrets) != 0 && s.Secrets == nil {
		return nil, errors.New("configuration references secrets but no resolver was supplied; set Options.Secrets")
	}
	for name, ref := range definition.Secrets {
		value, err := s.Secrets.Resolve(ref)
		if err != nil {
			return nil, err
		}
		values[name] = value
	}
	return values, nil
}

func (s DefaultSource) start(ctx context.Context, options providerproc.Options) (llm.Provider, error) {
	if s.startProcess != nil {
		return s.startProcess(ctx, options)
	}
	return providerproc.Start(ctx, options)
}

func (s DefaultSource) path(ctx context.Context, id string, definition config.Provider) (path string, ok bool, err error) {
	started := time.Now().UTC()
	llm.EmitObservation(ctx, s.Observer, llm.Observation{
		Operation: llm.ObservationProviderLocate, Phase: llm.ObservationStarted, Time: started,
		ProviderID: id, ProviderType: definition.Type, Version: definition.Version,
	})
	defer func() {
		llm.EmitObservation(ctx, s.Observer, llm.Observation{
			Operation: llm.ObservationProviderLocate, Phase: llm.ObservationFinished, Time: time.Now().UTC(),
			Duration: time.Since(started), ProviderID: id, ProviderType: definition.Type,
			Version: definition.Version, Err: err,
		})
	}()
	if definition.Path != "" {
		info, err := os.Stat(definition.Path)
		return definition.Path, err == nil && !info.IsDir(), err
	}
	if s.Locator != nil {
		path, ok, err := s.Locator.Locate(definition.Type, definition.Version)
		if err != nil || ok {
			return path, ok, err
		}
	}
	if !s.AllowPathLookup {
		return "", false, nil
	}
	path, err = exec.LookPath("go-inference-router-provider-" + definition.Type)
	return path, err == nil, nil
}

func providerConfig(definition config.Provider) (map[string]json.RawMessage, error) {
	values := make(map[string]any, len(definition.Options)+2)
	for key, value := range definition.Options {
		values[key] = value
	}
	values["base_url"] = definition.BaseURL
	values["headers"] = definition.Headers
	result := map[string]json.RawMessage{}
	for key, value := range values {
		if value == nil || value == "" {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode provider config %s: %w", key, err)
		}
		result[key] = encoded
	}
	return result, nil
}

func stringOption(options map[string]any, key string) string {
	value, _ := options[key].(string)
	return value
}

func providerEnvironment() []string {
	allowed := map[string]bool{
		"HOME": true, "USERPROFILE": true, "PATH": true, "TMPDIR": true, "TEMP": true, "TMP": true,
		"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "LANG": true, "LC_ALL": true,
	}
	var environment []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if allowed[key] {
			environment = append(environment, entry)
		}
	}
	return environment
}
