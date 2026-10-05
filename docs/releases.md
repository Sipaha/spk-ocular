# Builds and releases

## Distribution contract

SPK Ocular builds Linux, Windows and macOS for both amd64 and arm64. Each job
runs on a matching native host. Production desktop builds disable DevTools;
browser executables use CGO disabled.

| Platform | Native runners | Runtime | Artifacts per architecture |
| --- | --- | --- | --- |
| Linux | Ubuntu 24.04, Ubuntu 24.04 arm64 | glibc 2.39+, GTK 3, WebKit2GTK 4.1 | DEB, RPM, desktop tar.gz, browser tar.gz |
| Windows | Windows 2025 x64, Windows 11 arm64 | Windows 10/11, WebView2 Runtime | MSI, desktop ZIP, browser ZIP |
| macOS | macOS 15 Intel and Apple Silicon | macOS 12+ | DMG, desktop app tar.gz, browser tar.gz |

Names use `spk-ocular_VERSION_OS_ARCH.EXT`, or
`spk-ocular-browser_VERSION_OS_ARCH.EXT` for browser executables. OS names are
`linux`, `windows`, and `darwin`; architectures are `amd64` and `arm64`.

Windows MSI packages install per user, with a Start menu shortcut, and preserve
application data on removal. Executables have the GUI subsystem and embedded
icon/manifest. MSI stores the numeric MAJOR.MINOR.PATCH (maximum 255.255.65535);
the full SemVer remains in its display name, executable and archives. Same-base
prereleases can replace each other, in either order, as in the launcher's MSI.

macOS DMGs contain **SPK Ocular.app** and an Applications shortcut. The completed
bundle is ad-hoc signed and verified before packaging. It is not Developer ID
signed or notarized; macOS may require explicit approval under Privacy & Security.
Windows installers are unsigned. Publisher signing needs separately provisioned
certificates; no release credentials are fabricated or required for development
builds. In-app automatic updating remains planned.

Each file has a `.sha256` sidecar. The complete GitHub Release also includes
`SHA256SUMS`. Archives carry BUILD-INFO and the project license. Checksums detect
corrupt downloads; they are not independent publisher signatures.

The package and executable identity is `spk-ocular`; the displayed name is
**SPK Ocular**. DEB/RPM packages install the desktop entry, scalable/256 px icon,
a pixmap fallback, AppStream metadata, and license. Cache refresh hooks are
best-effort on install/removal. They never remove the user's data directory.

## Workflows

| Workflow | Trigger and purpose |
| --- | --- |
| `ci.yml` | PRs to master, pushes to master/release branches, manual runs |
| `test.yml` | Reusable full `make check` gate |
| `package-linux.yml` | Native Linux matrix and artifact validation |
| `package-native.yml` | Native Windows/macOS matrices, installer verification and desktop smoke |
| `release.yml` | Version-tag validation, tests, packaging and publication |

CI builds downloadable development packages after tests. Their version is the
VERSION file plus a development run identifier. These are workflow artifacts,
not published releases. Failed tests cannot produce release packages.

A pushed `vMAJOR.MINOR.PATCH` tag (optionally with a SemVer prerelease suffix)
starts the release workflow. The tag must match the VERSION file and the current
RELEASE_NOTES.md must start with `## SPK Ocular VERSION` and contain notes for
that version. The same test gate runs before all six native
package jobs. Architecture, contents, version and checksums are checked before
upload. Linux jobs install/remove DEB packages; Windows jobs install/remove MSI
packages; macOS jobs mount and verify the DMG and its sealed app bundle. Windows
and macOS jobs start the native webview with synthetic data and save a screenshot. The publish job verifies the complete expected
asset set, uploads everything to a draft, and checks the remote names and sizes
before publishing. A draft with unexpected assets is refused for manual review.
Published versions cannot be overwritten; use a new version for corrections.
Prerelease tags create GitHub prereleases.

Only the publish job receives `contents: write`; tests and builds are read-only.
Actions are pinned to reviewed commit SHAs. Go follows go.mod; Node, pnpm,
golangci-lint, actionlint nfpm and WiX are pinned in the workflow/setup files.
No personal access token or external signing secret is required for these
builds: publication uses the repository's GITHUB_TOKEN.

The implementation uses documented [reusable workflows](https://docs.github.com/en/actions/how-tos/reuse-automations/reuse-workflows),
[native ARM runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners),
and [Wails Linux packaging](https://v3.wails.io/guides/build/linux/).

## Prepare a version

1. Update VERSION and RELEASE_NOTES.md together. Notes describe the user-visible
   result compared with the previous published version, not intermediate fixes
   or the sequence of development commits. English and Russian are supported.
2. Run `make check` and `make package-linux RELEASE_VERSION=VERSION ARCH=ARCH`.
   Inspect artifacts with `packaging/verify.py` and open the packaged desktop
   binary in an isolated test profile. Never replace the user's running app.
3. Review and commit the intended changes. Use the project's required author
   and committer identity. Push and tag only within the user's authorized scope.
4. Create and push the matching version tag. Wait for Release to finish and
   verify the published asset set and checksums before announcing availability.

To reproduce a failed draft build, rerun the failed workflow jobs. An existing
draft can receive corrected uploads only for that same tagged source. If the
source must change, prepare a new tag/version rather than moving a published tag.

RELEASE_NOTES.md always describes the version in VERSION. Older release notes
belong in the published GitHub Releases and Git history, not a growing archive
of implementation reports in the working documentation.
