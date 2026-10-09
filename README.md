# SPK Ocular website

The static product website on the independent `pages` branch. The application
lives on `master`; this worktree does not change its builds or release history.

Astro generates eight complete website locales: Russian, English, simplified
Chinese, Spanish, German, French, Brazilian Portuguese and Japanese. Russian stays
at `/spk-ocular/`; the others use `/spk-ocular/{code}/`. The root chooses a saved
language, then ordered browser preferences, then English. Explicit localized URLs
are never overridden. The native-name language menu also works without JavaScript;
with JavaScript it remembers selection and preserves query/fragment. All routes
have canonical/hreflang metadata and sitemap entries. See
[localization quality and routing](docs/localization.md).
The site keeps Ocular’s logo, variable Inter for Latin/Cyrillic text, system fonts
for Chinese/Japanese, a blue accent and matching
light/dark colors. The SVG eye mark uses a blue optical lens, a restrained rim
and highlight; it remains legible at favicon and navigation sizes. Its product-led layout leads with Kubernetes + Docker,
a large real app screen and unrestricted free commercial use. The opening copy
identifies a desktop app for developers and DevOps, with local/no-account use
next to Download. Desktop hero columns align at the top, with a small optical
offset for the different heading/body font sizes. Task captions explain what each screenshot demonstrates. Investigation,
logs/terminal/forwarding, Helm and scoped local agent access have dedicated
sections. Connection retries and low-level persistence mechanics belong in the
application documentation, not the main marketing message.

The theme control uses a fixed-size Phosphor SVG; its MIT license is in
`public/licenses/phosphor-icons.txt` (upstream commit
`2b75f3ad12b420c9504ef05df8d2564a28f8500e`). Mobile navigation and Download remain
visible. Both desktop and mobile header navigation include a GitHub link to
the SPK Ocular repository. The link uses GitHub’s mark from Primer Octicons,
bundled locally; its MIT license is in `public/licenses/octicons.txt`. All content, full-size screenshots and download fallback links work
without JavaScript. Gallery tabs enhance static figures; FAQ answers stay visible.
Reduced motion, keyboard navigation and semantic light/dark colors are preserved.
Section anchors subtract the section’s responsive top padding from the scroll
offset, leaving 28px between the sticky header and the first section content.
Desktop, tablet and mobile offsets include their respective header/navigation height.
This works with native fragment links and JavaScript disabled.

## Product and funding message

Every feature is free for individuals and businesses of any size, revenue or
funding. There are no paid tiers or seat charges. Development is supported only
through voluntary cryptocurrency donations. Donations never unlock features.
The support section explains this policy and links to the owner’s personal
about site at `#support`, where donation details are maintained. The footer has
an About the author link. Desktop and mobile header navigation also include a
short Support link directly to the donation section. Both links target the matching about locale; Russian
uses an explicit `?lang=ru` choice. Wallet addresses and QR codes are not duplicated
on the Ocular website.

Local agent grants apply to processes running as the same OS user. Do not imply
separate authenticated agent identities or a security sandbox. Kubernetes and
Docker capabilities are distinct from providing a container runtime. The direct Kubernetes API, embedded Helm SDK and Ctrl+K/Command+K navigation
copy describes features present in the published v1.0.1 release. kubectl and a
separate Helm CLI are unnecessary; kubeconfig-configured external authentication
helpers still need to be installed. New product copy describes released features
without announcements or competitor comparisons. Docker includes standalone containers and Compose project/service
grouping; it does not supply a runtime. Standalone containers remain UI-only and
are not included in existing agent project grants. The gallery explicitly identifies development screenshots.

Design references: [Aptakube](https://aptakube.com/) for task-led feature stories
and large product screens, [Lens](https://lenshq.io/) for product hierarchy, and
[K8Studio](https://k8studio.io/) for investigation workflows (reviewed 2026-10-06).
No vendor claims, customer logos, testimonials or performance statistics are reused.

## Screenshot source

All twelve RU/EN product screenshots show SPK Ocular 1.1.0 on isolated fixtures.
Kubernetes captures use the owned ocular-dev demo cluster; Docker captures use the
verified ocular-dind engine, and Helm uses the explicitly labeled synthetic
release. The file screen shows an actual /app project with config, scripts, src,
public, tests, hidden files and a symlink, with application.yaml open.
Versioned image URLs prevent old cached screenshots from surviving the update.

## Develop and verify

Use Node 22 and pnpm 10.33.0. Install with `pnpm install --frozen-lockfile`.

```sh
pnpm dev
make check
node scripts/social.mjs # after build; rebuild to copy the generated public assets
node scripts/lighthouse.mjs
node scripts/server.mjs
```

`make check` runs release/OS logic tests, Astro checks, a clean static build, and
browser verification of all eight languages/themes at mobile, tablet and desktop
widths. It checks keyboard tabs, theme persistence, WCAG AA, layout and image
loading, free-use and donation copy, mobile section/download navigation, valid
anchor targets and content aligned 28px below the sticky header, API failure, JavaScript-disabled rendering, all 14 native release files, exclusion of browser-only builds
and architecture filters. Review its screenshots. `scripts/server.mjs` prints an
available loopback preview URL and stops on SIGTERM without orphan processes.

Set `OCULAR_SITE_SCRATCH` and `TMPDIR` to directories inside your workspace scratch
before checks. In the SPK-Ocular solution the default test output is
`.agents/tmp/site` at the solution root. Build products go to ignored `dist/`.

## Content and downloads

- `src/i18n/copy.ts`: RU/EN source dictionaries and the typed registry.
- `src/i18n/locales/*.json`: complete translations.
- `src/lib/languages.mjs`: names, paths and language matching.
- `src/components/Page.astro`: page sections and semantic no-JS HTML.
- `src/styles/global.css`: shared tokens and responsive layout.
- `src/scripts/site.ts`: theme, accessible gallery tabs and release enhancement.
- `src/lib/releases.mjs`: validated stable-release assets and OS detection.
- `public/media`: real product screenshots and rendered social previews.

Download links come from the public GitHub latest-release API for
`Sipaha/spk-ocular`. Files must match the release version, supported OS/architecture,
package format and repository URL. Missing checksums are not fabricated.
The OS and architecture are selected automatically when the browser provides
reliable hints. macOS's Intel user-agent text alone is not architecture evidence.
The primary buttons download the matching native Linux DEB, Windows MSI or
macOS DMG directly. Linux format priority matches the launcher website: DEB, then
RPM, with TAR.GZ as a last fallback. This is a default format policy, not distro
detection; alternative formats remain in the package list. Manual selectors
update the buttons.
When a compatible asset or reliable architecture is missing, main download
buttons say Choose and navigate to package selection. The header keeps a clear
Download caption and also opens package selection. Mobile/unknown devices keep the general choice.
Download buttons name the selected format (DEB, RPM, MSI, DMG, ZIP or TAR.GZ).
Hero and free-use download buttons include an All releases link directly below.
The narrow-screen header uses a short Download FORMAT/Download caption with a
complete accessible label. Without JavaScript, Choose links navigate to the download section.
Without a stable release or an available API, visible GitHub release and development-build
links remain. Three installation steps explain the installer formats, checksum
verification and existing Kubernetes/Docker configuration. Public release packages
do not require a GitHub account; CI development artifacts remain a separate path.
The website lists only native desktop packages; local browser-only builds are
not shown. Desktop installers appear before native portable archives,
regardless of GitHub upload order. No unmeasured memory or comparative size claims
are published. The site neither uploads user data nor starts any app connection.

`workloads-{ru,en}.webp`, `logs-{ru,en}.webp` and `health-{ru,en}.webp`
show read-only inspection of the disposable `kind-ocular-dev` / `ocular-demo`
fixture. `helm-{ru,en}.webp` shows the built-in synthetic Helm fixture, labeled
separately. `docker-{ru,en}.webp` shows a disposable standalone container
alongside Compose containers on the verified `ocular-dind` engine. Its temporary
container is removed after capture. Captures use the current `fc39a6e-dirty` development build with isolated
HOME, Docker, Ocular and kubeconfig directories. They contain no user environment
or credentials. WebP conversion preserves the real app image; the UI is not
reconstructed or generated. Social cards are rendered by `scripts/social.mjs`
from the built page's current copy, font and real screenshot.

Screenshots use demonstration data and are labeled accordingly. Product facts
must match `README.md` and `docs/usage.md` on `master`. When the product changes,
recapture screenshots from an isolated app profile, never from a user's cluster.

## Publish

The GitHub Pages URL is `https://sipaha.github.io/spk-ocular/`.
The repository is configured with **GitHub Actions** as its Pages source under
**Settings → Pages**. Custom domain configuration is not required for this URL.
The `github-pages` environment must also allow the `pages` branch under
**Settings → Environments → github-pages → Deployment branches and tags**.
An exact `pages` branch rule is configured alongside the existing `master` rule.
Preserve this rule and the other environment protections when changing deployment
settings. Upload and deployment actions are pinned to Node.js 24-compatible versions.

The website workflow validates, uploads and deploys the site on pushes to `pages`.
Deployment runs only when the repository has GitHub Actions configured as its
Pages source; if that configuration is removed, deployment is skipped while
checks and artifacts remain available.
The optional dispatch trigger also supports deployment when the workflow is
available on the default branch. No application tag or release is created.
Changing the public address requires updating `astro.config.mjs` and canonical,
language and social links in `src/layouts/Base.astro`.

## Container file inspector presentation

The feature section presents the container file inspector with actual localized
screens from an isolated Docker fixture. Copy identifies upcoming-release
availability, UTF-8/2 MiB editing limits, and the running Linux container/tool
requirements. File browsing does not require `ls`. All eight locales describe
Ocular's own behavior without competitor comparisons or exclusivity claims.
Public product copy must not name competing products to compare features or
claim that they lack a capability; this is the owner's publishing preference.
