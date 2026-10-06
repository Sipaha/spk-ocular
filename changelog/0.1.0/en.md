## SPK Ocular 0.1.0

A local desktop workspace for Kubernetes and Docker, available for Linux,
Windows and macOS on x86-64 and ARM64. All features are free for individuals
and companies, regardless of revenue or funding.

- Investigate live resources, events and relationships. Search within selected
  namespaces, keep favorites and open resource details alongside logs.
- Inspect standalone Docker containers and retain Compose project/service
  grouping. Read logs, open terminals and review container actions before running them.
- Manage Helm releases with embedded Helm: chart repositories and OCI registries,
  installation, upgrades, revision history, rollback and uninstall. Review
  chart versions, values and changes before applying them. No separate Helm CLI is needed.
- Give local coding agents scoped access using named permission groups.
  Pause access without losing its configuration; protected writes require review.
  Permissions apply to agents running as the same OS user. Standalone Docker
  containers and Helm remain UI-only and are not included in existing agent grants.
- Keep namespace selections, filters, favorites and column widths between visits.
  Use multiple log sources, terminals and local port forwarding in the same workspace.

### Download and install

Choose a desktop installer for your operating system and architecture:
DEB/RPM for Linux, MSI for Windows, or DMG for macOS. Portable desktop and local
browser-mode archives are also available. Each package has a `.sha256` sidecar;
`SHA256SUMS` lists every package checksum.

Linux requires Ubuntu 24.04+ or compatible glibc 2.39+, GTK 3 and WebKit2GTK 4.1.
Windows requires Windows 10/11 and WebView2. macOS requires version 12 or newer.
Windows packages are unsigned; macOS bundles are ad-hoc signed, not notarized.
Verify the source and checksum before approving installation.

Ocular uses existing Kubernetes and Docker environments; it does not provide
a container engine or virtual machine. Development is supported only through
optional cryptocurrency donations, which never unlock features.
