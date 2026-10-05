# SPK Ocular

SPK Ocular is a local desktop application for Kubernetes and Docker Compose.
It uses a native system webview and your existing connection configuration.
No account, hosted service, bundled cluster tools, or telemetry is required.

- Live resource tables, discovery of Kubernetes API resources, details, and metrics.
- Multiple namespaces, global favorites, resource search, and saved table layouts.
- Logs, interactive terminals, and local port forwarding.
- Reviewed Kubernetes changes, Secret value editing, and bulk actions.
- Explicit, locally managed access for automation agents.

## Install

Download a package matching your architecture from
[GitHub Releases](https://github.com/Sipaha/spk-ocular/releases).
If no release is published yet, use the source-build instructions below.
Desktop packages cover **Linux, Windows and macOS**, each for `amd64` (x86-64)
and `arm64` (AArch64 / Apple Silicon). Windows requires Windows 10/11 and
Microsoft Edge WebView2 Runtime; macOS requires 12 or newer.
Linux desktop packages target **Ubuntu 24.04 or newer**, or a compatible
Linux distribution with glibc 2.39+, GTK 3, and WebKit2GTK 4.1.

```sh
# Debian / Ubuntu; replace VERSION and ARCH with the downloaded filename.
sudo apt install ./spk-ocular_VERSION_linux_ARCH.deb

# Fedora and compatible RPM systems.
sudo dnf install ./spk-ocular_VERSION_linux_ARCH.rpm
```

Launch **SPK Ocular** from the application menu or run `spk-ocular`.
Upgrades use the same package name. Removing the package keeps your local data.
The archive contains an executable `spk-ocular`; its desktop runtime libraries
must already be installed. Verify a downloaded file with its `.sha256` sidecar:

```sh
sha256sum -c spk-ocular_VERSION_linux_ARCH.deb.sha256
```

On Windows, install `spk-ocular_VERSION_windows_ARCH.msi` for the current user,
or extract the desktop ZIP and open `spk-ocular.exe`. The MSI adds a Start menu
shortcut and keeps application data on removal. Windows installers are unsigned;
Windows may ask you to confirm the publisher before running them.

On macOS, open `spk-ocular_VERSION_darwin_ARCH.dmg` and drag **SPK Ocular.app**
to Applications. Bundles have an ad-hoc signature but are not Developer ID signed
or notarized. For a trusted download, macOS may require approval in
System Settings → Privacy & Security → Open Anyway.

Separate `spk-ocular-browser_VERSION_OS_ARCH` archives contain the browser-mode
binary (`.zip`/`.exe` on Windows, `.tar.gz` elsewhere). Extract and run:

```sh
./spk-ocular-browser --browser --port 5190
# Open http://127.0.0.1:5190 in a local browser.
```

The browser endpoint is local only. In-app automatic updates are not implemented.

## Connections and data

Kubernetes contexts come from `KUBECONFIG` (or `~/.kube/config`) and additional
kubeconfig files directly under `~/.kube`. Docker Compose connections come from
Docker contexts and the Docker environment variables. Connecting may execute
credential helpers configured in kubeconfig.

The application stores preferences, recent objects, and agent grants in
`~/.spk/ocular/ocular.db` (`%USERPROFILE%\.spk\ocular\ocular.db` on Windows). Set `SPK_OCULAR_HOME` to use another data directory.
Credentials stay in the connection configuration; Secret values are not stored
in the application database.

## Documentation

- [Using the application](docs/usage.md)
- [Architecture and behavior contracts](docs/architecture.md)
- [Local agent API](docs/agent-api.md)
- [Development and verification](docs/development.md)
- [Builds and releases](docs/releases.md)
- [Planned work](docs/backlog.md)
- [Contributor and coding-agent rules](AGENTS.md)

## Build from source

Requirements: the Go version in `go.mod`, Node.js 22, pnpm 10, a C toolchain,
`pkg-config`, GTK 3 and WebKit2GTK 4.1 development libraries.

```sh
sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
make build-desktop
./build/bin/spk-ocular-desktop
```

`make build` builds the browser-mode executable. `make check` runs the full
verification gate; see [development](docs/development.md) for its extra tools
and isolated scratch configuration. For native Windows/macOS packages, run
`python packaging/portable.py --version 0.1.0 --os windows --arch amd64` (use
`darwin` for macOS) on a matching native host. Windows needs WiX 5.0.2; macOS
needs Xcode Command Line Tools. CI runs on pull requests and pushes to
`master` or `release/**`; tagged versions use the same tests before packaging
and publication.

## License

[Apache License 2.0](LICENSE).
