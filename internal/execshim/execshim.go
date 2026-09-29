// Package execshim bounds kubeconfig exec credential plugins.
//
// client-go runs an exec plugin (yc, aws, gke-gcloud-auth-plugin,
// kubelogin, ...) with exec.Command and no context (v0.37,
// plugin/pkg/client/auth/exec): a plugin that hangs — waiting for a login,
// a dead network — blocks every request of that cluster regardless of
// request timeouts, and outlives the app as an orphan
// (docs/spikes/2026-09-29-exec-plugin-hang.md). Wrap rewrites a rest.Config
// so client-go runs our own binary as the plugin; the shim runs the real
// plugin with the same argv, env, stdin and stdout (the ExecCredential
// protocol passes through untouched), kills it after a timeout, and dies
// together with the app (Linux: parent-death signal), taking the plugin down.
package execshim

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

// Subcommand is the hidden CLI subcommand that runs Main.
const Subcommand = "exec-credential-shim"

// DefaultTimeout leaves room for a browser login (kubelogin, yc
// federation) while still ending a plugin that will never answer.
const DefaultTimeout = 90 * time.Second

// Wrap makes cfg's exec plugin (if any) run through the shim at shimPath.
// A no-op without an exec plugin or without a shim (tests, unusual builds).
func Wrap(cfg *rest.Config, shimPath string, timeout time.Duration) {
	if cfg == nil || cfg.ExecProvider == nil || shimPath == "" {
		return
	}
	ep := *cfg.ExecProvider // copy: the caller's config may be shared
	args := []string{Subcommand, "--timeout", timeout.String(), "--", ep.Command}
	ep.Args = append(args, ep.Args...)
	ep.Command = shimPath
	cfg.ExecProvider = &ep
}

// Main is the shim: `<Subcommand> --timeout D -- command args...`. It
// returns the process exit code.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(Subcommand, flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", DefaultTimeout, "kill the plugin after this long")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "exec-credential-shim: no plugin command")
		return 2
	}
	dieWithParent()

	cmd := exec.Command(rest[0], rest[1:]...)
	cmd.Env = os.Environ() // client-go set KUBERNETES_EXEC_INFO and the plugin's env on us
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	setChildAttrs(cmd)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "exec-credential-shim: %v\n", err)
		return 1
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		if err != nil {
			fmt.Fprintf(stderr, "exec-credential-shim: %v\n", err)
			return 1
		}
		return 0
	case <-time.After(*timeout):
		killTree(cmd)
		<-done
		fmt.Fprintf(stderr, "exec credential plugin %q did not answer within %s (a login it waits for, or no network); run it in a terminal to see why\n",
			strings.Join(rest, " "), *timeout)
		return 1
	}
}
