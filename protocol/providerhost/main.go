package providerhost

import (
	"context"
	"fmt"
	"io"
	"os"
)

// Main runs a Provider Process on stdio and exits nonzero on abnormal failure.
func Main(name, version string, factory Factory) {
	if err := run(context.Background(), os.Stdin, os.Stdout, version, factory); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, name+":", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, reader io.Reader, writer io.Writer, version string, factory Factory) error {
	err := Serve(ctx, reader, writer, version, factory)
	if NormalExit(err) {
		return nil
	}
	return err
}
