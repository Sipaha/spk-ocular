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
identifies a desktop app for developers and DevOps beside Download. The hero also
states that kubectl and a separate Helm CLI are unnecessary: Kubernetes access
is direct and Helm is embedded. The infrastructure section explicitly names
that no Ocular components need installation in the cluster; it does not promise
that all cluster services, authentication helpers or desktop runtimes are unnecessary. The hero
omits the local/no-account note at the owner’s request. Account absence is not
product positioning: the copy instead names existing kubeconfig/Docker access.
Future optional accounts or setup synchronization remain possible design choices,
not advertised or selected implementation work. Desktop hero columns align at the top, with a small optical
offset for the different heading/body font sizes. Task captions explain what each screenshot demonstrates. Investigation,
logs/terminal/forwarding, Security, Helm and scoped local agent access have dedicated
sections. Connection retries and low-level persistence mechanics belong in the
application documentation, not the main marketing message. Public pages omit
documentation/README guide links until the documentation is suitable for human
readers; this applies to navigation, feature sections, agent/install guidance
and the footer. Source, license, support and download links remain available.

The theme control uses a fixed-size Phosphor SVG; its MIT license is in
`public/licenses/phosphor-icons.txt` (upstream commit
`2b75f3ad12b420c9504ef05df8d2564a28f8500e`). Mobile navigation and Download remain
visible. Both desktop and mobile header navigation include a GitHub link to
the SPK Ocular repository. The link uses GitHub’s mark from Primer Octicons,
bundled locally; its MIT license is in `public/licenses/octicons.txt`. All content, full-size screenshots and download fallback links work
without JavaScript. Gallery tabs enhance static figures; FAQ answers stay visible. Screenshot links
open a modal preview without leaving the page. Images are centered in the full
viewport and fit all available width/height without enlarging beyond their native
1:1 size. The circular close button uses a centered SVG icon and overlays the preview
without reserving image space; its focus ring appears for keyboard interaction. Escape, the close button or the
backdrop dismiss it and restore focus and scroll position. Modified clicks and
JavaScript-disabled visits retain the original full-size image links.
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
short Support link directly to the donation section. Author and support links use the language-neutral about root; the destination
chooses its own language from saved choice and browser preferences. Wallet addresses and QR codes are not duplicated
on the Ocular website.

Local agent grants apply to processes running as the same OS user. Do not imply
separate authenticated agent identities or a security sandbox. Kubernetes and
Docker capabilities are distinct from providing a container runtime. The direct
Kubernetes API and embedded Helm SDK copy describes released features.
Ctrl+K/Command+K navigation includes type/name queries such as `dep model`
within selected namespaces. Security describes read-only workload configuration,
declared RBAC rules, NetworkPolicy selection and existing Trivy Operator reports.
It does not advertise live scanning, operator installation, exploitability or
compliance verification. These capabilities are present in the published application. kubectl and a
separate Helm CLI are unnecessary; kubeconfig-configured external authentication
helpers still need to be installed. New product copy describes released features
without announcements or competitor comparisons. Docker includes standalone containers and Compose project/service
grouping; it does not supply a runtime. Standalone containers remain UI-only and
are not included in existing agent project grants. The gallery identifies released-version screenshots and demonstration data.

Design references: [Aptakube](https://aptakube.com/) for task-led feature stories
and large product screens, [Lens](https://lenshq.io/) for product hierarchy, and
[K8Studio](https://k8studio.io/) for investigation workflows (reviewed 2026-10-06).
No vendor claims, customer logos, testimonials or performance statistics are reused.

## Screenshot source

All twenty-four RU/EN product screenshots show the actual SPK Ocular 1.7.0
production application in isolated profiles. Kubernetes and Docker use owned
synthetic HTTP API fixtures, without a real cluster, daemon or containers. Helm
and Security use the built-in synthetic provider; simulated findings do not
represent a live scan or real vulnerabilities. File browsing executes only the
fixture's bounded read-only scripts against its scratch demonstration tree.
The captures are real application UI, not interface mockups. Captions identify
simulated data. All product and social-image URLs use the 1.7.0 version query.
The other six website locales use the English screenshots with an explicit notice.

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
and architecture filters. It also checks the prominent memory metric outside
expanded conditions, outbound new-tab behavior with and without JavaScript,
generated package/checksum links and a real report popup. Review its screenshots. `scripts/server.mjs` prints an
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
Download buttons name the actual release version and selected format (DEB, RPM,
MSI, DMG, ZIP or TAR.GZ), using the same validated metadata as their file URL.
Package-list buttons also show that version.
Hero and free-use download buttons include an All releases link directly below.
The narrow-screen header uses a short Download VERSION/Download caption with a
complete accessible label. Without JavaScript, Choose links navigate to the download section.
Without a stable release or an available API, visible GitHub release and development-build
links remain. Three installation steps explain the installer formats, checksum
verification and existing Kubernetes/Docker configuration. The compatibility block
describes the released 1.6.1 DEB/RPM glibc 2.35 floor and Ubuntu 24.04 build
baseline. Normal package installation and desktop/browser startup are verified
on Ubuntu 22.04 and Debian 12/13 (DEB), and Fedora 44 (RPM) on x86-64, plus
native Ubuntu 22.04 ARM64. GTK 3/WebKit2GTK 4.1 remain required in both launch
modes. The isolated userspace/shared-kernel/X11 checks do not certify stock
kernels, Wayland or GPU drivers. Windows/macOS requirements and
installer signing status remain visible. Public release packages
do not require a GitHub account; CI development artifacts remain a separate path.
The website lists only native desktop packages; local browser-only builds are
not shown. Desktop installers appear before native portable archives,
regardless of GitHub upload order. No unmeasured memory or comparative size claims
are published. The site neither uploads user data nor starts any app connection.

`workloads`, `logs`, `health`, `graph`, `timeline`, `rbac`, `compare` and
`workspace` show the isolated Kubernetes HTTP fixture. `docker` and `files` show
standalone and Compose resources from the isolated Engine HTTP fixture. `helm`
and `security` use explicitly simulated built-in provider data. All captures
use isolated profiles without user credentials or configuration. WebP conversion
preserves the captured application image. Social cards are rendered by
`scripts/social.mjs` from the current page copy, font and product screenshot.

Screenshots use demonstration data and are labeled accordingly. Product facts
must match `README.md` and `docs/usage.md` on `master`. When the product changes,
recapture screenshots from an isolated app profile, never from a user's cluster.

## Repository About

The GitHub repository description is:

> Free Kubernetes and Docker desktop app for Windows, macOS, and Linux. Built-in Helm, logs, terminals, and file transfers. No kubectl or Helm CLI required.

Changing this metadata requires an authenticated GitHub session or API credential
with repository administration access. An SSH push alone does not update About.

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
screens from an isolated Docker fixture. Copy identifies UTF-8/2 MiB editing
limits and the running Linux container/tool requirements. File browsing does not require `ls`. All eight locales describe
Ocular's own behavior without competitor comparisons or exclusivity claims.
Public product copy must not name competing products to compare features or
claim that they lack a capability; this is the owner's publishing preference.

## Bounded inventory copy

All eight locales describe the released 1.7.0 demand-kind/selected-namespace
inventory, automatic small pages, shared compact caches/watch, bounded request
concurrency/rate and idle retention. The All Namespaces safeguard asks for a
narrower selection instead of presenting partial results as complete. Refresh,
elapsed time, consumed response-body data and cancellation describe live resource
lists; graph admission is explicit. This copy does not claim zero cluster impact,
universal memory use or completed production-scale certification. The current long-soak
stability result is NOT ACCEPTED in the application measurement report.

## Large-inventory evidence

All eight locales lead with the measured 10,000-Pod workload and a rounded
558 MiB sampled peak of combined application and Chromium PSS. The metric is
visible without opening the conditions, together with Linux/browser-mode,
version 1.7.0 and six-session scope. The complete list supports local search and
sorting. A 9.8-second median load and configured 10 QPS budget remain visible;
the rate is not observed throughput or a whole-process memory cap.

Warm-return LIST counts and internal stability verdicts are not product sales
copy. The original reports and raw samples remain linked and unchanged; current
technical documentation separately records reassessment with the owner-selected
15 MiB rebound tolerance. No stable-memory or universal-performance guarantee is
made. The site contains neither unpublished-build CPU comparisons nor statements
about absence of production testing. Measurement inputs are in
`src/data/measurements.json`.

All outbound links open a new tab with `noopener noreferrer`, including static
reports/author/source links without JavaScript, generated release packages and
checksums, and primary buttons when they resolve to an external package. Local
section/language links retain their current-tab behavior. Image links retain
the existing modal preview.
