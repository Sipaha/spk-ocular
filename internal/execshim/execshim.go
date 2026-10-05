// Package execshim bounds kubeconfig exec credential plugins.
//
// client-go runs an exec plugin (yc, aws, gke-gcloud-auth-plugin,
// kubelogin, ...) with exec.Command and no context (v0.37,
// plugin/pkg/client/auth/exec): a plugin that hangs — waiting for a login,
// a dead network — blocks every request of that cluster regardless of
// request timeouts, and outlives the app as an orphan
// (docs/architecture.md). Wrap rewrites a rest.Config
// so client-go runs our own binary as the plugin; the shim runs the real
// plugin with the same argv, env, stdin and stdout (the ExecCredential
// protocol passes through untouched), kills it after a timeout, and dies
// together with the app (Linux: parent-death signal), taking the plugin down.
package execshim

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

// Subcommand is the hidden CLI subcommand that runs Main.
const Subcommand = "exec-credential-shim"

// DefaultTimeout leaves room for a browser login (kubelogin, yc
// federation) while still ending a plugin that will never answer.
const DefaultTimeout = 90 * time.Second

// HoldTimeout bounds a headless run (under a hold): a silent refresh is
// quick; a plugin waiting for a person will not get one.
const HoldTimeout = 15 * time.Second

// headlessEnv: what a plugin could reach a person with — a browser, a
// desktop prompt (a portal, pinentry over D-Bus), a terminal, an askpass.
// Linux; elsewhere (macOS open) a headless run is not guaranteed.
var headlessEnv = []string{"DISPLAY", "WAYLAND_DISPLAY", "DBUS_SESSION_BUS_ADDRESS", "BROWSER", "GPG_TTY", "SSH_ASKPASS"}

// Wrap makes cfg's exec plugin (if any) run through the shim at shimPath.
// A no-op without an exec plugin or without a shim (tests, unusual builds).
func Wrap(cfg *rest.Config, shimPath string, timeout time.Duration) {
	WrapHeld(cfg, shimPath, timeout, "")
}

// WrapHeld is Wrap with a hold file (P18): while it exists (the session is
// in the background) the plugin runs headless — stdin empty, no way to a
// person, not interactive, within HoldTimeout — so a silent refresh works
// and a login that needs a person fails instead of popping up.
func WrapHeld(cfg *rest.Config, shimPath string, timeout time.Duration, hold string) {
	WrapSession(cfg, shimPath, timeout, hold, "")
}

// WrapSession also binds plugins to a session lifetime file. Removing it
// cancels running helpers and refuses late credential requests for that session.
func WrapSession(cfg *rest.Config, shimPath string, timeout time.Duration, hold, lifetime string) {
	if cfg == nil || cfg.ExecProvider == nil || shimPath == "" {
		return
	}
	ep := *cfg.ExecProvider // copy: the caller's config may be shared
	args := []string{Subcommand, "--timeout", timeout.String()}
	if hold != "" {
		args = append(args, "--hold", hold)
	}
	if lifetime != "" {
		args = append(args, "--lifetime", lifetime)
	}
	args = append(args, "--", ep.Command)
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
	hold := fs.String("hold", "", "while this file exists, run the plugin headless")
	holdTimeout := fs.Duration("hold-timeout", HoldTimeout, "the timeout of a headless run")
	lifetime := fs.String("lifetime", "", "stop when this session lifetime file disappears")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		_, _ = fmt.Fprintln(stderr, "exec-credential-shim: no plugin command")
		return 2
	}
	dieWithParent()
	sessionAlive := func() bool {
		if *lifetime == "" {
			return true
		}
		_, err := os.Stat(*lifetime)
		return err == nil
	}
	if !sessionAlive() {
		_, _ = fmt.Fprintln(stderr, "exec credential plugin: connection cancelled")
		return 1
	}

	cmd := exec.Command(rest[0], rest[1:]...)
	cmd.Env = os.Environ() // client-go set KUBERNETES_EXEC_INFO and the plugin's env on us
	held := false
	if *hold != "" {
		_, err := os.Stat(*hold)
		held = err == nil
	}
	if held {
		cmd.Env = headless(cmd.Env)
		stdin = strings.NewReader("")
		*timeout = min(*timeout, *holdTimeout)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	setChildAttrs(cmd)
	if err := cmd.Start(); err != nil {
		_, _ = fmt.Fprintf(stderr, "exec-credential-shim: %v\n", err)
		return 1
	}
	kill, release, err := containChild(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_, _ = fmt.Fprintf(stderr, "exec-credential-shim: contain plugin: %v\n", err)
		return 1
	}
	defer release()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.NewTimer(*timeout)
	defer deadline.Stop()
	var check <-chan time.Time
	if *lifetime != "" {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		check = ticker.C
	}
	for {
		select {
		case err := <-done:
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return ee.ExitCode()
			}
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "exec-credential-shim: %v\n", err)
				return 1
			}
			return 0
		case <-check:
			if !sessionAlive() {
				kill()
				<-done
				_, _ = fmt.Fprintln(stderr, "exec credential plugin: connection cancelled")
				return 1
			}
		case <-deadline.C:
			kill()
			<-done
			if held {
				_, _ = fmt.Fprintf(stderr, "exec credential plugin %q did not answer within %s while its target is in the background (a login needs a person: select the target and press Connect)\n",
					strings.Join(rest, " "), *timeout)
				return 1
			}
			_, _ = fmt.Fprintf(stderr, "exec credential plugin %q did not answer within %s (a login it waits for, or no network); run it in a terminal to see why\n",
				strings.Join(rest, " "), *timeout)
			return 1
		}
	}
}

// headless is env without a way to a person, and KUBERNETES_EXEC_INFO
// saying the run is not interactive.
func headless(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		switch {
		case slices.Contains(headlessEnv, name):
			continue
		case name == "KUBERNETES_EXEC_INFO":
			kv = name + "=" + notInteractive(value)
		}
		out = append(out, kv)
	}
	return out
}

// notInteractive sets spec.interactive=false in an ExecCredential (as it
// is; one that does not parse is left to the plugin).
func notInteractive(info string) string {
	var cred map[string]any
	if json.Unmarshal([]byte(info), &cred) != nil {
		return info
	}
	spec, _ := cred["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
	}
	spec["interactive"] = false
	cred["spec"] = spec
	b, err := json.Marshal(cred)
	if err != nil {
		return info
	}
	return string(b)
}
