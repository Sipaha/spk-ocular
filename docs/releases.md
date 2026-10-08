# Builds and releases

Current patch release: [1.0.1](https://github.com/Sipaha/spk-ocular/releases/tag/v1.0.1), built with Go 1.26.8. Its complete native matrix, published package contents and checksums are verified. The native evidence archive contains owned-window screenshots for all six targets; the live site exposes matching package and checksum links. Published 1.0.0 assets remain immutable.

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

Each file has a `.sha256` sidecar. `NATIVE-VERIFICATION.zip` contains all six owned production-window screenshots and matching source/version reports with synthetic test data, excluding app logs and credentials. The complete GitHub Release also includes
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
starts the release workflow. The tag is the release version, as in the launcher;
it does not need to match the development VERSION file. The description comes from
`changelog/<version>/en.md`, with a `Release <version>` fallback if absent.
Localized descriptions live alongside it, such as `ru.md`. The same test gate
runs before all six native package jobs. Architecture, contents, version and checksums are checked before
upload. Linux jobs install/remove DEB packages; Windows jobs install/remove MSI
packages; macOS jobs mount and verify the DMG and its sealed app bundle. All six jobs start the native webview with synthetic data and save a screenshot. The publish job verifies the complete expected
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

## Third-party licenses

`make build-web` collects exact installed frontend production dependencies and
Tailwind's generated CSS license and Vite/Rolldown's injected runtime helpers. `packaging/licenses.py` uses Go's selected
production package graphs for browser and desktop builds on all six supported
targets, including the Go runtime, upstream `NOTICE` files and legal texts in
used package trees. Test-only dependencies are not selected. SQLite's inherited
third-party notices are retained. Missing legal texts stop the build; new
frontend licenses other than MIT require review. The Wails npm runtime omits its
license text, so the collector verifies its version against the Go module and
uses that module's upstream MIT license.
License files use explicit POSIX relative-name order on every platform. CI scans reachable Go vulnerabilities with the pinned patched SDK.
Go JSON and environment output are decoded explicitly as UTF-8, independently
of the Windows system code page.
macOS DMG creation reserves explicit filesystem headroom based on the staged
payload rather than relying on `hdiutil`'s minimal automatic size estimate.

The generated `THIRD-PARTY-NOTICES.txt` is committed with dependency changes and
embedded in every binary. Run `make licenses` to regenerate it and
`python3 packaging/licenses.py --check` to check freshness after collecting the
frontend inputs. `spk-ocular licenses` prints the embedded document without
opening a window or connecting to infrastructure.

Every archive includes the document; DEB/RPM install it under
`/usr/share/doc/spk-ocular`, MSI beside the executable, and macOS bundles under
`Contents/Resources`. Artifact verification compares the external and embedded
texts to the source document, and native MSI verification checks its installation
and removal. The app's Apache-2.0 license does not replace component licenses.
The adapted Helm SQL storage files preserve upstream headers and explicitly
identify Ocular's modifications.

## Prepare a version

1. Add `changelog/<version>/en.md` and `ru.md`. Notes describe the user-visible
   result compared with the previous published version, not intermediate fixes
   or the sequence of development commits. VERSION is the default development
   base; tagged builds take their version directly from the tag.
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

The versioned `changelog/<version>/<locale>.md` files are the release-note source,
matching the launcher. Published notes remain in their version directory; current
product behavior belongs in the main documentation.
