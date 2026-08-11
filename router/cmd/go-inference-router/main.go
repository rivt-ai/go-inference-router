package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/rivt-ai/go-inference-router/protocol/jsonrpc"
	"github.com/rivt-ai/go-inference-router/router"
	"github.com/rivt-ai/go-inference-router/router/configfile"
	"github.com/rivt-ai/go-inference-router/router/rpcserver"
	"github.com/rivt-ai/go-inference-router/router/secret"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "go-inference-router:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) != 0 && args[0] == "secret" {
		return runSecret(args[1:], stdin, stdout)
	}
	flags := flag.NewFlagSet("go-inference-router", flag.ContinueOnError)
	flags.SetOutput(stderr)
	explicit := flags.String("config", "", "use only this models YAML file")
	workspace := flags.String("workspace", "", "workspace containing .go-inference-router/models.yaml")
	if err := flags.Parse(args); err != nil {
		return err
	}
	resolver, err := secret.DefaultResolver()
	if err != nil {
		return err
	}
	server := rpcserver.New(stdin, stdout)
	modelRouter, err := router.Open(ctx, router.Options{
		Loader:  configfile.Loader(*workspace, *explicit),
		Secrets: resolver, Observer: server, Stderr: stderr,
	})
	if err != nil {
		return err
	}
	defer func() { _ = modelRouter.Close() }()
	var installerAPI rpcserver.Installer
	if providerInstaller := modelRouter.Installer(); providerInstaller != nil {
		installerAPI = providerInstaller
	}
	err = server.Serve(ctx, modelRouter, installerAPI, modelRouter.Reload)
	if jsonrpc.IsClosed(err) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func runSecret(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) < 1 {
		return errors.New("usage: go-inference-router secret <set|list|delete> [name]")
	}
	storePath, identityPath, err := secret.DefaultPaths()
	if err != nil {
		return err
	}
	identity, err := secret.LoadOrCreateIdentity(secret.OSKeychain{}, identityPath, storePath)
	if err != nil {
		return err
	}
	store := secret.NewEncryptedStore(storePath, identity)
	switch args[0] {
	case "set":
		if len(args) != 2 {
			return errors.New("usage: go-inference-router secret set <name>")
		}
		value, err := secret.ReadAll(stdin)
		if err != nil {
			return err
		}
		return store.Set(args[1], value)
	case "list":
		names, err := store.List()
		if err != nil {
			return err
		}
		for _, name := range names {
			_, _ = fmt.Fprintln(stdout, name)
		}
		return nil
	case "delete":
		if len(args) != 2 {
			return errors.New("usage: go-inference-router secret delete <name>")
		}
		return store.Delete(args[1])
	default:
		return fmt.Errorf("unknown secret command %q", args[0])
	}
}
