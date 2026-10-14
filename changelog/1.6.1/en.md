# SPK Ocular 1.6.1

- Linux DEB/RPM packages require glibc 2.35 or newer instead of 2.39. Ubuntu 22.04 and Debian 12 can install the packages normally when GTK 3 and WebKit2GTK 4.1 are available.
- Both Linux architectures receive native Ubuntu 22.04 installation, browser-mode and GTK-window checks before publication. Packaging also rejects executable symbols above the declared glibc floor.
- The native verification bundle adds both Ubuntu 22.04 reports and screenshots alongside the existing six-platform evidence. These are X11/userspace checks on the runner kernel; they do not certify every kernel, GPU driver or Wayland session.
