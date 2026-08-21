package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Plan restates what an approved install would download, in plain Go so a
// host's command layer does not import the protocol types.
type Plan struct {
	Provider string
	Version  string
	Source   string
	Size     int64
	SHA256   string
}

// ConfirmFunc decides one install. It receives the verified plan and returns
// whether to proceed; approval stays host policy, only the choreography around
// it lives here.
type ConfirmFunc func(Plan) (bool, error)

// ErrDeclined reports that the host's ConfirmFunc rejected the plan.
var ErrDeclined = errors.New("install declined")

// Install plans provider@version, puts the plan to confirm, and installs the
// verified artifact on approval, returning the installed binary path.
//
// This is the plan → show a human → approve sequence every host repeats;
// wrapping it keeps plan IDs and expiry out of host code. A nil confirm
// installs without asking, for hosts whose approval happened earlier.
func (i *Installer) Install(ctx context.Context, provider, version string, confirm ConfirmFunc) (string, error) {
	plan, err := i.Plan(ctx, provider, version)
	if err != nil {
		return "", err
	}
	if confirm != nil {
		ok, err := confirm(Plan{
			Provider: plan.Provider, Version: plan.Version, Source: plan.Source,
			Size: plan.Size, SHA256: plan.SHA256,
		})
		if err != nil {
			return "", fmt.Errorf("confirm install of %s %s: %w", plan.Provider, plan.Version, err)
		}
		if !ok {
			return "", fmt.Errorf("install %s %s: %w", plan.Provider, plan.Version, ErrDeclined)
		}
	}
	return i.Approve(ctx, plan.ID)
}

// DefaultCacheDir is where managed Provider Process artifacts live.
//
// It is exported because the installer that writes the cache and the router
// that reads it must agree on this path; two entry points computing it
// separately would install to one place and look in another.
func DefaultCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "go-inference-router", "providers"), nil
}
