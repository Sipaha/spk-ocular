# Builds and releases

## Distribution contract

SPK Ocular ships Linux amd64 and arm64 builds. Desktop packages use native
Ubuntu 24.04 runners, GTK 3 and WebKit2GTK 4.1; the runtime baseline is glibc 2.39.
The production build disables DevTools. The separate browser executable is
built with CGO disabled. macOS, Windows and automatic in-app updating are planned
separately; no unsupported platform is advertised as a working release.

For each architecture and version:

- `spk-ocular_VERSION_linux_ARCH.deb`
- `spk-ocular_VERSION_linux_ARCH.rpm`
- `spk-ocular_VERSION_linux_ARCH.tar.gz` (desktop executable and supporting files)
- `spk-ocular-browser_VERSION_linux_ARCH.tar.gz` (static browser-mode executable)

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
| `package-linux.yml` | Reusable native architecture matrix and artifact validation |
| `release.yml` | Version-tag validation, tests, packaging and publication |

CI builds downloadable development packages after tests. Their version is the
VERSION file plus a development run identifier. These are workflow artifacts,
not published releases. Failed tests cannot produce release packages.

A pushed `vMAJOR.MINOR.PATCH` tag (optionally with a SemVer prerelease suffix)
starts the release workflow. The tag must match the VERSION file and the current
RELEASE_NOTES.md must start with `## SPK Ocular VERSION` and contain notes for
that version. The same test gate runs before both native
package jobs. Architecture, contents, checksums and a real DEB install/remove
are verified before upload. The publish job verifies the complete expected
asset set, uploads everything to a draft, and checks the remote names and sizes
before publishing. A draft with unexpected assets is refused for manual review.
Published versions cannot be overwritten; use a new version for corrections.
Prerelease tags create GitHub prereleases.

Only the publish job receives `contents: write`; tests and builds are read-only.
Actions are pinned to reviewed commit SHAs. Go follows go.mod; Node, pnpm,
golangci-lint, actionlint and nfpm are pinned in the workflow/setup files.
No personal access token or external signing secret is required for these Linux
releases: publication uses the repository's GITHUB_TOKEN.

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
