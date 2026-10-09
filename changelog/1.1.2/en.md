# SPK Ocular 1.1.2

- Build with Go 1.26.9 and golang.org/x/net v0.60.0 to address the newly reported HTTP/2 vulnerabilities.
- Browse container files in Kubernetes and Docker: a cached folder tree, symlink targets, syntax highlighting and protected UTF-8 editing up to 2 MiB. In the desktop app, copy complete files or folders through a native directory chooser; transfers are streamed and existing destination names are never overwritten.
- Keep logs in a separate native window while retaining a single stream. Choose the destination for plain-text log exports. All log levels are visible; text filtering and search remain available.
- Choose a Pod for multi-instance workloads and a container for multi-container Pods. Right-click Logs, Terminal or Files for explicit setup; only Terminal includes a command field. Single options open directly on left-click and remain visible in setup.
- Inspect retained Deployment revisions and compare read-only Pod-template YAML with the current Deployment or another revision.
- Use shortcuts by physical key across keyboard layouts, including editor commands. Fix repeated Cyrillic terminal input, defer terminal resize updates until drag release, and restore separator focus after dragging.
- Improve file-inspector space, tree resizing, cached entry counts, loading/empty rows and error overlays. Related resources have compact kind/name rows; log window actions use icons at the right edge.

Container file operations require a running Linux container with the necessary tools: sh for browsing, readlink for symlinks, and tar for downloads. Native downloads do not use the editor's 2 MiB limit. Browser-mode container downloads require the desktop app. Unsafe archive entries and destination collisions are refused.

Native packages are provided for Linux, Windows and macOS on amd64 and arm64. This release does not change the existing installer signing/notarization status or rewrite older release assets.
