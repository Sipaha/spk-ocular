# SPK Ocular website

The static product website on the independent `pages` branch. The application
lives on `master`; this worktree does not change its builds or release history.

Astro generates Russian (`/spk-ocular/`) and English (`/spk-ocular/en/`) pages.
The site uses the application's existing logo, Inter, a blue accent, light/dark
semantic colors, and real screenshots from an isolated demonstration profile.
The hero names Kubernetes and Docker directly; copy describes concrete product
capabilities instead of echoing the launcher’s one-click slogan.
Motion is limited to initial hierarchy and interaction feedback; reduced motion
is respected. Native disclosure elements and all content work without JavaScript.

## Develop and verify

Use Node 22 and pnpm 10.33.0. Install with `pnpm install --frozen-lockfile`.

```sh
pnpm dev
make check
node scripts/lighthouse.mjs
node scripts/server.mjs
```

`make check` runs release/OS logic tests, Astro checks, a clean static build, and
browser verification of both languages/themes at mobile, tablet and desktop
widths. It checks keyboard tabs, theme persistence, WCAG AA, layout and image
loading, API failure, JavaScript-disabled rendering, all 20 release files and
architecture filters. Review its screenshots. `scripts/server.mjs` prints an
available loopback preview URL and stops on SIGTERM without orphan processes.

Set `OCULAR_SITE_SCRATCH` and `TMPDIR` to directories inside your workspace scratch
before checks. In the SPK-Ocular solution the default test output is
`.agents/tmp/site` at the solution root. Build products go to ignored `dist/`.

## Content and downloads

- `src/i18n/copy.ts`: both dictionaries, with a shared type.
- `src/components/Page.astro`: page sections and semantic no-JS HTML.
- `src/styles/global.css`: shared tokens and responsive layout.
- `src/scripts/site.ts`: theme, accessible gallery tabs and release enhancement.
- `src/lib/releases.mjs`: validated stable-release assets and OS detection.
- `public/media`: real product screenshots and rendered social previews.

Download links come from the public GitHub latest-release API for
`Sipaha/spk-ocular`. Files must match the release version, supported OS/architecture,
package format and repository URL. Missing checksums are not fabricated. The OS
may be suggested; architecture is always an explicit visitor choice. Without a
stable release or an available API, visible GitHub release and development-build
links remain. The site neither uploads user data nor starts any app connection.

Screenshots use demonstration data and are labeled accordingly. Product facts
must match `README.md` and `docs/usage.md` on `master`. When the product changes,
recapture screenshots from an isolated app profile, never from a user's cluster.

## Publish

The intended GitHub Pages URL is `https://sipaha.github.io/spk-ocular/`.
The repository currently needs Pages enabled with **GitHub Actions** as its source
under **Settings → Pages**. This requires authenticated repository administration;
SSH source pushes alone cannot change that setting.

The website workflow validates and uploads the site on pushes to `pages`.
Deployment runs only when the repository already has GitHub Actions configured
as its Pages source. Until then it is skipped, while checks and artifacts remain
available. After configuring Pages, the next push to `pages` publishes the site.
The optional dispatch trigger also supports deployment when the workflow is
available on the default branch. No application tag or release is created.
Changing the public address requires updating `astro.config.mjs` and canonical,
language and social links in `src/layouts/Base.astro`.
