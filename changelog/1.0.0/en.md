## SPK Ocular 1.0.0

A local desktop workspace for Kubernetes and Docker, with native packages for
Linux, Windows and macOS on x86-64 and ARM64.

### Changes since 0.1.0

- Use Ocular in Russian, English, Simplified Chinese, Spanish, German, French,
  Brazilian Portuguese or Japanese. The native-name language menu remembers
  your choice across restarts; switching language keeps workspaces and edits.
  Resource identifiers, raw data, logs and manifests remain in their original
  language. Documentation and existing screenshots remain in English.
- Explicit connections stay open while reading Helm charts, editing values,
  minimizing the window or switching targets. Configuration changes automatically
  check a replacement connection. Existing review/version checks still protect
  writes; recovery never repeats a mutation.
- Helm now uses the shared resource table and detail panel, including sorting,
  remembered column widths and a draggable divider. Agent-permission panels also
  resize. Empty states appear inside tables; object details keep their layout
  under a loading overlay, with stale controls disabled.
- Inspect and manage explicitly linked kubeconfig files or encrypted custom
  configurations. Review changes and protect unsaved resource edits; passwords
  and plaintext credentials are not stored in UI preferences.
- Open About from the Ocular name and icon in the header. View the running build,
  Apache 2.0 license, project links and Pavel Simonov’s author profile. The product
  icon is consistent across the website, browser and native application.
- Distributions include exact third-party license notices, alongside the
  application’s Apache 2.0 license.

### Download and install

Choose the installer matching your operating system and architecture: DEB/RPM
for Linux, MSI for Windows or DMG for macOS. Portable desktop and local browser
archives are also available. Every package has a `.sha256` sidecar; `SHA256SUMS`
lists the complete set.

Linux requires Ubuntu 24.04+ or compatible glibc 2.39+, GTK 3 and WebKit2GTK 4.1.
Windows requires Windows 10/11 and WebView2. macOS requires version 12 or newer.
Windows packages are unsigned; macOS bundles are ad-hoc signed and not notarized.
Ocular uses existing Kubernetes and Docker environments; it does not provide a
container engine or virtual machine. In-app automatic updating remains planned.
