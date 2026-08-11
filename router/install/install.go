// Package install verifies and atomically installs Provider Process artifacts.
package install

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	llm "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/protocol/llmv1"
	"golang.org/x/mod/semver"
)

const (
	manifestLimit = 4 << 20
	planLifetime  = 10 * time.Minute
)

// Manifest is the signed registry payload.
type Manifest struct {
	Version   int        `json:"version"`
	Artifacts []Artifact `json:"artifacts"`
}

// Artifact identifies one platform-specific Provider Process binary.
type Artifact struct {
	Provider string `json:"provider"`
	Version  string `json:"version"`
	Protocol string `json:"protocol"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	URL      string `json:"url"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

type pendingPlan struct {
	artifact Artifact
	expires  time.Time
}

// Installer plans, verifies, and caches Provider Process artifacts.
type Installer struct {
	registry  string
	publicKey ed25519.PublicKey
	cache     string
	client    *http.Client
	observer  llm.Observer
	planMu    sync.Mutex
	cacheMu   sync.RWMutex
	plans     map[string]pendingPlan
}

// New creates an Installer rooted at cache.
func New(registry string, publicKey ed25519.PublicKey, cache string, client *http.Client, observer llm.Observer) *Installer {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Installer{
		registry: registry, publicKey: publicKey, cache: cache, client: client,
		observer: observer, plans: map[string]pendingPlan{},
	}
}

// Plan verifies the registry and creates a short-lived one-use approval plan.
func (i *Installer) Plan(ctx context.Context, provider, version string) (result llmv1.InstallPlan, resultErr error) {
	event := i.start(ctx, llm.ObservationInstallPlan, provider, version)
	defer func() { i.finish(ctx, event, resultErr) }()
	artifact, err := i.artifact(ctx, provider, version)
	if err != nil {
		return llmv1.InstallPlan{}, err
	}
	id, err := randomID()
	if err != nil {
		return llmv1.InstallPlan{}, err
	}
	expires := time.Now().Add(planLifetime)
	i.planMu.Lock()
	i.plans[id] = pendingPlan{artifact: artifact, expires: expires}
	i.planMu.Unlock()
	return llmv1.InstallPlan{
		ID: id, Provider: provider, Version: artifact.Version, Source: artifact.URL,
		Size: artifact.Size, SHA256: artifact.SHA256, Expires: expires.UTC().Format(time.RFC3339),
	}, nil
}

// Approve consumes a plan and installs its verified artifact.
func (i *Installer) Approve(ctx context.Context, planID string) (path string, resultErr error) {
	event := i.start(ctx, llm.ObservationInstallApprove, "", "")
	i.planMu.Lock()
	plan, ok := i.plans[planID]
	delete(i.plans, planID)
	i.planMu.Unlock()
	event.ProviderID, event.Version = plan.artifact.Provider, plan.artifact.Version
	defer func() { i.finish(ctx, event, resultErr) }()
	if !ok {
		return "", &llm.Error{Kind: llm.KindInvalidRequest, Provider: "installer", Message: "unknown or already-used install plan"}
	}
	if time.Now().After(plan.expires) {
		return "", &llm.Error{Kind: llm.KindInvalidRequest, Provider: plan.artifact.Provider, Message: "install plan expired"}
	}
	// A download only adds a version directory, so installs and lookups run
	// concurrently. The read lock is held to exclude Remove, which takes the
	// write lock and must never delete a version mid-install.
	i.cacheMu.RLock()
	defer i.cacheMu.RUnlock()
	return i.download(ctx, plan.artifact)
}

// Available compares cached versions with the newest stable registry artifact.
func (i *Installer) Available(ctx context.Context, provider string) (result llmv1.InstallAvailableResponse, resultErr error) {
	event := i.start(ctx, llm.ObservationInstallAvailable, provider, "")
	defer func() { i.finish(ctx, event, resultErr) }()
	// A provider the registry no longer offers still has a reclaimable cache
	// inventory, so an absent artifact reports an empty available version
	// instead of failing the whole query.
	artifact, err := i.artifact(ctx, provider, "")
	if err != nil && !llm.IsKind(err, llm.KindUnavailable) {
		return result, err
	}
	i.cacheMu.RLock()
	versions, err := i.installedVersions(provider)
	i.cacheMu.RUnlock()
	if err != nil {
		return result, err
	}
	result = llmv1.InstallAvailableResponse{
		Provider: provider, InstalledVersions: versions, AvailableVersion: artifact.Version,
	}
	for _, version := range versions {
		if stableVersion(version) {
			result.InstalledVersion = version
			break
		}
	}
	result.UpdateAvailable = result.AvailableVersion != "" && (result.InstalledVersion == "" ||
		semver.Compare(normalizedVersion(result.InstalledVersion), normalizedVersion(result.AvailableVersion)) < 0)
	return result, nil
}

// Remove deletes one exact cached Provider Process version.
func (i *Installer) Remove(ctx context.Context, provider, version string) (removed bool, resultErr error) {
	event := i.start(ctx, llm.ObservationInstallRemove, provider, version)
	defer func() { i.finish(ctx, event, resultErr) }()
	if !safeName(provider) || !safeVersion(version) {
		return false, invalidVersionError(provider)
	}
	i.cacheMu.Lock()
	defer i.cacheMu.Unlock()
	dir := filepath.Join(i.cache, provider, version)
	if _, err := os.Lstat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, err
	}
	_ = os.Remove(filepath.Join(i.cache, provider))
	return true, nil
}

func (i *Installer) start(ctx context.Context, operation llm.ObservationOperation, provider, version string) llm.Observation {
	event := llm.Observation{
		Operation: operation, Phase: llm.ObservationStarted, Time: time.Now().UTC(),
		ProviderID: provider, Version: version,
	}
	llm.EmitObservation(ctx, i.observer, event)
	return event
}

func (i *Installer) finish(ctx context.Context, event llm.Observation, err error) {
	event.Phase, event.Duration, event.Time = llm.ObservationFinished, time.Since(event.Time), time.Now().UTC()
	event.Err = err
	llm.EmitObservation(ctx, i.observer, event)
}

func (i *Installer) artifact(ctx context.Context, provider, version string) (Artifact, error) {
	if !safeName(provider) || (version != "" && !safeVersion(version)) {
		return Artifact{}, invalidVersionError(provider)
	}
	manifest, err := i.manifest(ctx)
	if err != nil {
		return Artifact{}, err
	}
	var matches []Artifact
	for _, artifact := range manifest.Artifacts {
		if !matchesRequest(artifact, provider, version) {
			continue
		}
		if err := validArtifact(artifact); err != nil {
			return Artifact{}, &llm.Error{Kind: llm.KindProtocol, Provider: provider, Message: err.Error()}
		}
		matches = append(matches, artifact)
	}
	if len(matches) == 0 {
		return Artifact{}, &llm.Error{Kind: llm.KindUnavailable, Provider: provider, Message: "no compatible provider artifact"}
	}
	sort.Slice(matches, func(a, b int) bool { return versionLess(matches[a].Version, matches[b].Version) })
	return matches[len(matches)-1], nil
}

func matchesRequest(artifact Artifact, provider, version string) bool {
	if artifact.Provider != provider || artifact.Protocol != llmv1.Protocol ||
		artifact.OS != runtime.GOOS || artifact.Arch != runtime.GOARCH {
		return false
	}
	if version != "" {
		return artifact.Version == version
	}
	return stableVersion(artifact.Version)
}

func validArtifact(artifact Artifact) error {
	if !safeVersion(artifact.Version) || artifact.Size < 0 || !secureURL(artifact.URL) {
		return errors.New("registry contains an invalid artifact")
	}
	if _, err := hex.DecodeString(artifact.SHA256); err != nil || len(artifact.SHA256) != sha256.Size*2 {
		return errors.New("registry contains an invalid SHA-256")
	}
	return nil
}

func invalidVersionError(provider string) error {
	return &llm.Error{Kind: llm.KindInvalidRequest, Provider: provider, Message: "invalid provider or version"}
}

// Locate finds and re-verifies an exact or latest installed provider binary.
func (i *Installer) Locate(provider, version string) (string, bool, error) {
	if !safeName(provider) || (version != "" && !safeVersion(version)) {
		return "", false, invalidVersionError(provider)
	}
	i.cacheMu.RLock()
	defer i.cacheMu.RUnlock()
	if version != "" {
		return i.locateExact(provider, version, false)
	}
	versions, err := i.installedVersions(provider)
	if err != nil || len(versions) == 0 {
		return "", false, err
	}
	for _, candidate := range versions {
		if stableVersion(candidate) {
			return i.locateExact(provider, candidate, true)
		}
	}
	return "", false, nil
}

func (i *Installer) installedVersions(provider string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(i.cache, provider))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	versions := []string{}
	for _, entry := range entries {
		if entry.IsDir() && safeVersion(entry.Name()) {
			versions = append(versions, entry.Name())
		}
	}
	sort.Slice(versions, func(a, b int) bool { return versionLess(versions[b], versions[a]) })
	return versions, nil
}

func (i *Installer) locateExact(provider, version string, required bool) (string, bool, error) {
	path := i.path(provider, version)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !required {
			return "", false, nil
		}
		return "", false, &llm.Error{Kind: llm.KindProtocol, Provider: provider, Message: "installed provider binary is missing", Err: err}
	}
	if !info.Mode().IsRegular() {
		return "", false, &llm.Error{Kind: llm.KindProtocol, Provider: provider, Message: "installed provider binary is not a regular file"}
	}
	if err := verifyDigest(path, provider); err != nil {
		return "", false, err
	}
	return path, true, nil
}

func (i *Installer) manifest(ctx context.Context) (Manifest, error) {
	if !secureURL(i.registry) || len(i.publicKey) != ed25519.PublicKeySize {
		return Manifest{}, errors.New("provider registry URL or public key is invalid")
	}
	body, err := i.fetch(ctx, i.registry, manifestLimit)
	if err != nil {
		return Manifest{}, err
	}
	signatureText, err := i.fetch(ctx, i.registry+".sig", 4096)
	if err != nil {
		return Manifest{}, err
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signatureText)))
	if err != nil || !ed25519.Verify(i.publicKey, body, signature) {
		return Manifest{}, &llm.Error{Kind: llm.KindProtocol, Provider: "registry", Message: "registry signature verification failed"}
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil || manifest.Version != 1 {
		return Manifest{}, &llm.Error{Kind: llm.KindProtocol, Provider: "registry", Message: "invalid registry manifest"}
	}
	return manifest, nil
}

func (i *Installer) download(ctx context.Context, artifact Artifact) (string, error) { //nolint:gocyclo // every failure closes and removes the temporary artifact
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return "", err
	}
	response, err := i.client.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("download provider: HTTP %d", response.StatusCode)
	}
	destination := i.path(artifact.Provider, artifact.Version)
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, ".provider-*")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(0o700); err != nil {
		_ = file.Close()
		return "", err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, artifact.Size+1))
	if err != nil {
		_ = file.Close()
		return "", err
	}
	if written != artifact.Size || hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(artifact.SHA256) {
		_ = file.Close()
		return "", &llm.Error{Kind: llm.KindProtocol, Provider: artifact.Provider, Message: "provider artifact size or SHA-256 mismatch"}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return "", err
	}
	if err := writeDigest(destination, strings.ToLower(artifact.SHA256)); err != nil {
		_ = os.Remove(destination)
		return "", err
	}
	return destination, nil
}

func writeDigest(path, digest string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".digest-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := io.WriteString(file, digest+"\n"); err != nil {
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
	return os.Rename(temporary, path+".sha256")
}

func verifyDigest(path, provider string) error {
	expected, err := os.ReadFile(path + ".sha256")
	if err != nil {
		return &llm.Error{Kind: llm.KindProtocol, Provider: provider, Message: "provider integrity record is missing", Err: err}
	}
	digest := strings.TrimSpace(string(expected))
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return &llm.Error{Kind: llm.KindProtocol, Provider: provider, Message: "provider integrity record is invalid", Err: err}
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(digest) {
		return &llm.Error{Kind: llm.KindProtocol, Provider: provider, Message: "provider binary SHA-256 mismatch"}
	}
	return nil
}

func (i *Installer) fetch(ctx context.Context, source string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	response, err := i.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: HTTP %d", source, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("registry response exceeds size limit")
	}
	return data, nil
}

func (i *Installer) path(provider, version string) string {
	name := "go-inference-router-provider-" + provider
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(i.cache, provider, version, runtime.GOOS+"-"+runtime.GOARCH, name)
}

func secureURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	host := parsed.Hostname()
	return parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")
}

func safeName(value string) bool {
	return safeIdentifier(value, "._-")
}

func safeVersion(value string) bool {
	return safeIdentifier(value, "._-+")
}

func safeIdentifier(value, punctuation string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, char := range value {
		letter := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
		digit := char >= '0' && char <= '9'
		if !letter && !digit && !strings.ContainsRune(punctuation, char) {
			return false
		}
	}
	return true
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func stableVersion(version string) bool {
	version = normalizedVersion(version)
	return semver.IsValid(version) && semver.Prerelease(version) == ""
}

func versionLess(a, b string) bool {
	left, right := normalizedVersion(a), normalizedVersion(b)
	leftValid, rightValid := semver.IsValid(left), semver.IsValid(right)
	if leftValid != rightValid {
		return !leftValid
	}
	if compared := semver.Compare(left, right); compared != 0 {
		return compared < 0
	}
	return a < b
}

func normalizedVersion(version string) string {
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}
