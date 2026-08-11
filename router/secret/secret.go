// Package secret resolves configured secret references and owns the encrypted
// local secret store.
package secret

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"filippo.io/age"
	"github.com/rivt-ai/go-inference-router/router/config"
	keyring "github.com/zalando/go-keyring"
)

const (
	keychainService = "go-inference-router"
	storeKeyAccount = "encrypted-secret-store"
	storeKeyEnv     = "INFROUTER_SECRET_STORE_KEY"
)

// ErrNotFound means a named secret does not exist.
var ErrNotFound = errors.New("secret not found")

// ErrUnavailable means the OS keychain cannot be reached on this machine.
var ErrUnavailable = errors.New("OS keychain unavailable")

// Keychain is the native secret-store seam used in production and tests.
type Keychain interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
	Delete(service, account string) error
}

// OSKeychain uses the platform credential store.
type OSKeychain struct{}

// Get reads a native keychain entry.
func (OSKeychain) Get(service, account string) (string, error) {
	value, err := keyring.Get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if keychainUnavailable(err) {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return value, err
}

// Set writes a native keychain entry.
func (OSKeychain) Set(service, account, value string) error {
	err := keyring.Set(service, account, value)
	if keychainUnavailable(err) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return err
}

// Delete removes a native keychain entry.
func (OSKeychain) Delete(service, account string) error {
	err := keyring.Delete(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	if keychainUnavailable(err) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return err
}

// Resolver resolves all supported SecretRef variants.
type Resolver struct {
	Keychain Keychain
	Store    *EncryptedStore
	lazy     *lazyStore
}

type lazyStore struct {
	once         sync.Once
	keychain     Keychain
	storePath    string
	identityPath string
	store        *EncryptedStore
	err          error
}

// NewResolver creates a Resolver that opens the encrypted store only when a
// secret reference first needs it.
func NewResolver(keychain Keychain, storePath, identityPath string) Resolver {
	return Resolver{
		Keychain: keychain,
		lazy:     &lazyStore{keychain: keychain, storePath: storePath, identityPath: identityPath},
	}
}

// DefaultPaths returns the OS locations of the encrypted store and the identity
// that opens it.
func DefaultPaths() (storePath, identityPath string, err error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", "", err
	}
	dir = filepath.Join(dir, "go-inference-router")
	return filepath.Join(dir, "secrets.age"), filepath.Join(dir, "secrets.key"), nil
}

// DefaultResolver resolves credential references against the OS keychain and
// the encrypted store at their default locations.
//
// Calling this is how a host opts in to the age, go-keyring, and dbus
// dependencies; router.Options.Secrets is deliberately not defaulted to it.
func DefaultResolver() (Resolver, error) {
	storePath, identityPath, err := DefaultPaths()
	if err != nil {
		return Resolver{}, err
	}
	return NewResolver(OSKeychain{}, storePath, identityPath), nil
}

// Resolve returns a secret without logging or persisting its plaintext.
func (r Resolver) Resolve(ref config.SecretRef) (string, error) {
	switch {
	case ref.Env != "":
		value, ok := os.LookupEnv(ref.Env)
		if !ok {
			return "", fmt.Errorf("environment secret %q: %w", ref.Env, ErrNotFound)
		}
		return value, nil
	case ref.Keychain != "":
		if r.Keychain == nil {
			return "", errors.New("OS keychain is unavailable")
		}
		return r.Keychain.Get(keychainService, ref.Keychain)
	case ref.Secret != "":
		store, err := r.encryptedStore()
		if err != nil {
			return "", err
		}
		if store == nil {
			return "", errors.New("encrypted secret store is unavailable")
		}
		return store.Get(ref.Secret)
	case ref.File != "":
		return readFile(ref.File)
	default:
		return "", errors.New("empty secret reference")
	}
}

func (r Resolver) encryptedStore() (*EncryptedStore, error) {
	if r.Store != nil {
		return r.Store, nil
	}
	if r.lazy == nil {
		return nil, nil
	}
	r.lazy.once.Do(func() {
		identity, err := LoadOrCreateIdentity(r.lazy.keychain, r.lazy.identityPath, r.lazy.storePath)
		if err != nil {
			r.lazy.err = err
			return
		}
		r.lazy.store = NewEncryptedStore(r.lazy.storePath, identity)
	})
	return r.lazy.store, r.lazy.err
}

func readFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("secret file %s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("secret file %s must be owner-only", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r"), nil
}

// EncryptedStore is an age-encrypted named-value file.
type EncryptedStore struct {
	path     string
	identity *age.X25519Identity
	mu       sync.Mutex
}

// NewEncryptedStore opens or lazily creates path.
func NewEncryptedStore(path string, identity *age.X25519Identity) *EncryptedStore {
	return &EncryptedStore{path: path, identity: identity}
}

// Get reads one encrypted entry.
func (s *EncryptedStore) Get(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.load()
	if err != nil {
		return "", err
	}
	value, ok := values[name]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

// Set atomically writes one encrypted entry.
func (s *EncryptedStore) Set(name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.load()
	if err != nil {
		return err
	}
	values[name] = value
	return s.save(values)
}

// Delete removes one encrypted entry.
func (s *EncryptedStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := values[name]; !ok {
		return ErrNotFound
	}
	delete(values, name)
	return s.save(values)
}

// List returns sorted entry names without values.
func (s *EncryptedStore) List() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values, err := s.load()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (s *EncryptedStore) load() (map[string]string, error) {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	reader, err := age.Decrypt(file, s.identity)
	if err != nil {
		return nil, fmt.Errorf("decrypt secret store: %w", err)
	}
	values := map[string]string{}
	if err := json.NewDecoder(reader).Decode(&values); err != nil {
		return nil, fmt.Errorf("decode secret store: %w", err)
	}
	return values, nil
}

func (s *EncryptedStore) save(values map[string]string) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".secrets-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	writer, err := age.Encrypt(file, s.identity.Recipient())
	if err != nil {
		_ = file.Close()
		return err
	}
	if err := json.NewEncoder(writer).Encode(values); err != nil {
		_ = writer.Close()
		_ = file.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, s.path)
}

// LoadOrCreateIdentity resolves the encrypted-store key from an explicit
// environment override or the OS keychain, creating a keychain entry lazily.
func LoadOrCreateIdentity(chain Keychain, fallbackPath, storePath string) (*age.X25519Identity, error) {
	if value := os.Getenv(storeKeyEnv); value != "" {
		return age.ParseX25519Identity(value)
	}
	if fallbackPath != "" {
		value, err := readFile(fallbackPath)
		if err == nil {
			return age.ParseX25519Identity(value)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if chain == nil {
		return fallbackIdentity(fallbackPath, storePath, nil)
	}
	value, err := chain.Get(keychainService, storeKeyAccount)
	if err == nil {
		return age.ParseX25519Identity(value)
	}
	if errors.Is(err, ErrUnavailable) {
		return fallbackIdentity(fallbackPath, storePath, nil)
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, err
	}
	if err := chain.Set(keychainService, storeKeyAccount, identity.String()); err != nil {
		if errors.Is(err, ErrUnavailable) {
			return fallbackIdentity(fallbackPath, storePath, identity)
		}
		return nil, err
	}
	return identity, nil
}

func fallbackIdentity(path, storePath string, identity *age.X25519Identity) (*age.X25519Identity, error) {
	if path == "" {
		return nil, errors.New("OS keychain is unavailable and " + storeKeyEnv + " is unset")
	}
	if _, err := os.Stat(storePath); err == nil {
		return nil, errors.New("OS keychain is unavailable and the existing encrypted store has no fallback identity")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if identity == nil {
		var err error
		identity, err = age.GenerateX25519Identity()
		if err != nil {
			return nil, err
		}
	}
	if err := writeIdentity(path, identity.String()); err != nil {
		if errors.Is(err, os.ErrExist) {
			value, readErr := readFile(path)
			if readErr != nil {
				return nil, readErr
			}
			return age.ParseX25519Identity(value)
		}
		return nil, err
	}
	return identity, nil
}

func writeIdentity(path, value string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := io.WriteString(file, value+"\n"); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

// ReadAll exists for CLI stdin secret entry without imposing a prompt library.
func ReadAll(reader io.Reader) (string, error) {
	var buffer bytes.Buffer
	if _, err := io.Copy(&buffer, reader); err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(buffer.String(), "\n"), "\r"), nil
}
