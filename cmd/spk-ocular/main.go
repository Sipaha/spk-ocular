// Command spk-ocular is a lightweight local infrastructure viewer: a desktop
// window by default, or the same UI over HTTP on localhost with --browser.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/spk/spk-ocular/internal/execshim"
)

// version is set at build time: -ldflags "-X main.version=...".
var version = "dev"

type browserOpts struct {
	Port    int
	TestAPI bool
	// TestSynthetic adds the synthetic provider (e2e of the log UI); only
	// with TestAPI.
	TestSynthetic bool
}

type runners struct {
	browser func(ctx context.Context, o browserOpts) error
	desktop func(ctx context.Context, o browserOpts) error
}

func newRootCmd(run runners) *cobra.Command {
	var o browserOpts
	var browser bool
	root := &cobra.Command{
		Use:           "spk-ocular",
		Short:         "Lightweight local infrastructure viewer (Kubernetes first)",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.TestSynthetic && !o.TestAPI {
				return fmt.Errorf("--test-synthetic needs --test-api")
			}
			if browser {
				return run.browser(cmd.Context(), o)
			}
			return run.desktop(cmd.Context(), o)
		},
	}
	root.Flags().BoolVar(&browser, "browser", false, "Serve the UI over HTTP on localhost instead of opening a window")
	root.Flags().IntVar(&o.Port, "port", 5190, "HTTP port for --browser")
	root.Flags().BoolVar(&o.TestAPI, "test-api", false, "Expose /api/_test/* automation routes (development/e2e only; desktop: on a loopback port written to test-api.json in the data directory)")
	root.Flags().BoolVar(&o.TestSynthetic, "test-synthetic", false, "Add a synthetic test provider (e2e only; needs --test-api)")
	_ = root.Flags().MarkHidden("test-synthetic")
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "spk-ocular", version)
		},
	})
	return root
}

func main() {
	// client-go runs this binary as a bounded exec credential plugin
	// (internal/execshim); it must not start the app.
	if len(os.Args) > 1 && os.Args[1] == execshim.Subcommand {
		os.Exit(execshim.Main(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	tuneGoMemory(os.Getenv)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cmd := newRootCmd(runners{browser: runBrowser, desktop: runDesktop})
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
