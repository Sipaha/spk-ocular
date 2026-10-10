# Website direction

The product website lives on the independent `pages` branch, checked out in
`.site`. The redesign is published at
[the product homepage](https://sipaha.github.io/spk-ocular/).
Keep website and application worktrees separate. Authorized pushes to `pages`
run the website checks and GitHub Pages deployment. Website publication does not
publish application releases or authorize wallet publication.

## Publication policy

Public website feature descriptions must correspond to features included in a
published application release. Unreleased feature announcements are not approved.
The file-inspector section is retained at the owner's request; its copy matches
the released 1.1.2 implementation.

The released-capability copy explains direct Kubernetes API access without
kubectl, embedded Helm SDK use without a separate Helm CLI, and Ctrl+K/Command+K
navigation. kubeconfig-configured external authentication helpers remain required.
All eight locales describe these released v1.1.2 capabilities without competitor
comparisons or exclusivity claims.

## Implemented presentation

The Astro website retains Russian and English routes, light/dark themes,
canonical/hreflang/OG metadata, bundled fonts and progressive enhancement.
Its main message is **Kubernetes and Docker in one workspace**, supported
by real product screens and daily tasks rather than connection-button mechanics.

The page now presents:

- An explicit desktop-app introduction for developers and DevOps, with local
  operation and no-account access beside Download.
- A large localized app screenshot with a concrete task caption.
- Investigation through resource health, events, relationships, configuration and
  read-only Deployment revision comparison.
- Related logs, search, native log windows, plain-text exports, instance/container
  selection, terminals and port forwarding, without promising complete observation
  or unrestricted log history.
- A cached container file tree, symlink navigation, protected UTF-8 editing and
  native streamed file/folder downloads with explicit runtime requirements.
- Docker standalone containers with Compose project/service grouping, with the
  runtime boundary and UI-only standalone access stated explicitly.
- The implemented Helm lifecycle, repositories and embedded SDK.
- Local agent access with scope/operation controls and an explicit same-OS-user
  trust boundary, without claiming authenticated per-agent identities.
- Open-source local operation, free commercial use and optional donation funding.
- An accessible full-size image gallery, mobile section navigation, installation
  requirements, signature status and visible FAQ answers.
- A three-step installation guide and separate release/development download paths.

Screenshots show version 1.1.2 on an isolated local Kubernetes
fixture and a verified disposable Docker engine, including standalone containers
and Compose grouping; Helm uses a separately labeled synthetic fixture. Screenshots are real
app captures, not reconstructed mockups. Stable packages are
available through [GitHub Releases](https://github.com/Sipaha/spk-ocular/releases/latest);
the site selects validated assets from the latest stable release.

The release selector preserves repository/version/asset validation, explicit
architecture selection, actual checksum links, an eight-second request timeout
and no-JavaScript/offline fallback links. Development-build guidance explains
that GitHub login may be needed and artifacts expire. Public release downloads
do not require an account. No memory or comparative size claim is published
without reproducible measurements for the stated platform and scenario.

## Confirmed funding model

All features are free for individuals and companies, regardless of revenue or
funding. There is no paid tier, trial expiry, seat charge or feature paywall.
Development is supported only through voluntary cryptocurrency donations.
Donations do not unlock features or imply investment returns, ownership or an SLA.

Download is the primary action. Support development is a secondary section/link.
There are no wallet addresses, networks or QR codes until the owner supplies
verified details. Never block downloads with a donation prompt. Explain this
product promise separately from the Apache-2.0 license.

## Reference strategy

[Aptakube](https://aptakube.com/) informs task-led feature stories and large product
screens; [Lens](https://lenshq.io/) informs clear product hierarchy;
[K8Studio](https://k8studio.io/) informs investigation workflows. Primary pages
were reviewed on 2026-10-06. Their copy, customer logos, testimonials, performance
claims and paid business models are not reused. This is a presentation reference,
not evidence of measured conversion uplift for Ocular.

## Remaining work before promotion

- Keep validating the anonymous download path and checksums for each release.
  Native CI checks DEB/MSI installation and removal, macOS DMG/bundle verification,
  and native UI startup. These checks cover the configured runner environments,
  not every supported OS version or user machine. Signing/notarization remains
  a separate distribution improvement.
- Add dedicated quickstart, trust and task guides as the documentation and release
  paths mature; extend sitemap/structured metadata to match actual routes.
- Consider a real Compose workflow and agent grant/review demonstration in the
  gallery. Capture them on disposable fixtures and label development versions.
- Add owner-verified donation details only when provided and authorized; check
  the network, copyable address and QR content against that source.

## Verification and feedback

The website's `make check` runs release logic tests, Astro checks/build and real
browser checks across both languages, themes and desktop/tablet/mobile widths.
The browser verifier covers image loading, keyboard gallery navigation, theme
persistence, WCAG AA checks, free-use messaging, mobile navigation, anchors,
no-JavaScript rendering, API failure and package/architecture/checksum selection.
Inspect actual screenshots; automated accessibility checks are not a complete
manual accessibility audit. Keep run logs and screenshots in solution scratch,
not in maintained documentation. The site README documents the exact workflow.

Recruit an opt-in first-use cohort and observe installation, first inspection and
one diagnosis/review task. Check whether visitors understand free commercial use
and optional donations without coaching. App telemetry and website analytics
remain separate decisions; no collection is implied. Prefer useful task guides,
truthful demonstrations and tested package channels to broad AI claims.

The container file inspector section uses fresh RU/EN screenshots from an
isolated Docker demo project with an expanded application tree. Its 1.1.2 copy
describes syntax highlighting, save protection, symlink navigation and streamed
native file/folder copies, with explicit sh/readlink/tar, editor-size, desktop-only
and destination-collision requirements. All twelve product images and both social
cards are refreshed; versioned image URLs avoid stale cached media.
The owner's public-copy preference is to describe Ocular's own capabilities
without named competitor comparisons or claims of exclusivity.
