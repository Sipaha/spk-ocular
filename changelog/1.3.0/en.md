# SPK Ocular 1.3.0

## Security workspace

- Inspect Kubernetes workload hardening, ServiceAccount token inheritance, declared role risks and NetworkPolicy ingress/egress selection under Cluster → Security.
- Read existing Trivy Operator vulnerability reports with scanner, report date, image, package and available fix evidence. Missing reports show installation guidance; permission and read failures remain visible.
- Security is read-only and bounded. Saved reports are not a live scan; declarations do not prove effective permissions, network enforcement, exploitability or compliance. No scanner is installed automatically.
- Use the shared virtual resource table with sorting, keyboard navigation and saved resizable columns. All eight interface languages are supported.

## Navigation and interface

- Resource relationships use compact aligned Type / Name / Open tables.
- Ctrl+K supports type/name queries such as `dep model` and `pod model` within the selected target and scopes. Recent history ranks matching live resources without introducing deleted or replaced objects.
- Known table headings remain visible during initial loading. Inspector close controls are centered, and Escape cancels column resizing without saving the drag.

## Distribution

- One native executable opens the desktop window by default or serves a loopback interface with `--browser --port 5190`. Separate browser-only distributions are no longer produced.
- Packages cover Linux, Windows and macOS on amd64 and arm64. Linux browser mode retains GTK/WebKit runtime dependencies. Existing signing and installation limitations remain unchanged.
