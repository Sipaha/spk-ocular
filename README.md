# SPK Ocular

A local desktop workspace for Kubernetes and Docker, with a native system
webview and an optional loopback browser interface. No Ocular account is
required. All features are free for individuals and companies.

[Website](https://sipaha.github.io/spk-ocular/) ·
[Downloads and release notes](https://github.com/Sipaha/spk-ocular/releases/latest)

## Install and run

Download a package for your operating system and architecture. Linux, Windows
and macOS packages are available for amd64 and arm64. Consult the selected
release's notes for its included features.

- Linux: glibc 2.35+, GTK 3 and WebKit2GTK 4.1. Install the DEB/RPM package with
  your distribution's package manager, or use the archive with those libraries.
- Windows: Windows 10/11 with Microsoft Edge WebView2 Runtime. Install the MSI
  or extract the ZIP. Installers are unsigned.
- macOS: macOS 12+. Open the DMG and move SPK Ocular.app to Applications.
  Bundles are ad-hoc signed, without Developer ID signing or notarization.

Start SPK Ocular from the application menu. For browser mode, run:

```sh
spk-ocular --browser --port 5190
```

Open `http://127.0.0.1:5190`. Select a connection and press **Connect**.
Connecting can execute credential helpers from your kubeconfig. Choose only
configuration and targets you trust. In-app updates are not implemented.

## Build from source

Use the Go version declared in `go.mod`, Node.js 22 and pnpm 10. Linux desktop
builds also need a C compiler, pkg-config, GTK 3 and WebKit2GTK 4.1 development
libraries. With the required toolchain installed:

```sh
make build-desktop
make check
```

Contributor instructions are in [AGENTS.md](AGENTS.md). Detailed project
documentation, research and planning are maintained separately in a private
repository. Public builds and checks do not require that checkout.

## License

[Apache License 2.0](LICENSE). Dependencies retain their own licenses; see
[third-party notices](THIRD-PARTY-NOTICES.txt), also available with
`spk-ocular licenses`.
