# Linux distribution compatibility

The published native executable uses GTK 3 and WebKit2GTK 4.1. These libraries
remain necessary in both desktop and `--browser` modes. Ocular does not bundle a
Linux distribution or a webview runtime.

## Package requirements and observed compatibility

From 1.6.1, DEB and RPM declare **glibc 2.35 or newer**. The earlier published
1.6.0 packages retain their original glibc 2.39 declaration. Ubuntu 24.04 is
the native Linux build and CI baseline, not the only usable distribution.
Package declarations and the minimum libraries needed by the executable are
separate questions.

Both Linux architectures must pass normal DEB installation, browser-mode startup
without a display and an actual GTK synthetic resource view in a SHA256-pinned
Ubuntu 22.04 userspace with glibc 2.35 before release publication. The executable
symbol-version guard also refuses a package whose binary needs newer glibc.
Ubuntu 24.04 remains the native build runner; the older-userspace runtime check
uses that runner's kernel and private Xvfb/X11.

The current amd64 distribution matrix is:

| Distribution | glibc | Native package | Browser and GTK startup |
| --- | --- | --- | --- |
| Ubuntu 22.04 | 2.35 | DEB | Verified |
| Debian 12 | 2.36 | DEB | Verified |
| Debian 13 | 2.41 | DEB | Verified |
| Fedora 44 | 2.43 | RPM | Verified |

The 1.6.0 amd64 archive already passed these runtime checks; its DEB refused the
first two environments because of its stricter declared floor. The corrected
1.6.1 declaration removes that packaging restriction without replacing system
glibc or using dependency force flags. Published 1.6.0 assets remain immutable.

The runtime library packages are `libgtk-3-0` (or its `t64` variant) and
`libwebkit2gtk-4.1-0` on these DEB systems, and `gtk3`/`webkit2gtk4.1` on Fedora.
Ubuntu's WebKit package is in the official universe repository.

## Verification limits

These are isolated official distribution root filesystems using a shared host
kernel, private DBus, Xvfb and synthetic application data. They verify the
listed userspace, library and X11 startup paths; they do not certify each
distribution's stock kernel, desktop compositor, GPU driver or Wayland session.
They are not complete Kubernetes/Docker integration tests or a full regression
suite on each distribution. No real clusters or user container engines were used.

The Debian/Fedora matrix covers amd64 only. Ubuntu 22.04 compatibility gates run
natively for both amd64 and arm64, and both reports/screenshots accompany the
six-platform native release evidence. These results do not certify unlisted
distributions, older glibc versions, stock kernels, GPU drivers or Wayland.
