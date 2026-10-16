# Website localization

The website has complete dictionaries for Russian, English, simplified Chinese,
Spanish, German, French, Brazilian Portuguese and Japanese. Additional-language pages use actual English screenshots and explicitly
state that the screenshot UI is in English. Public documentation links are
intentionally omitted until reader-facing documentation is ready. Repository
documentation remains available for development and maintenance.

## Quality threshold

The owner prioritizes natural, accurate copy over a large language selector.
Translations are authored and checked against the existing product copy. Preserve
all conditions and limitations: free commercial use, voluntary cryptocurrency-only
support, shared same-OS-user agent permissions, no isolation promise, existing
Docker engine required, installer signing status and precise platform requirements.
Never shorten those conditions away for a more attractive claim.

No independent native-speaker certification has been performed. Automated parity,
technical-token and layout checks do not prove every sentence is idiomatic. Known
unresolved mistranslations block publishing that locale. Review feedback from
native-speaking technical users when available and keep terminology consistent.
Persian is deferred until an adequate terminology and language-review process is
available. This is not a blanket claim that AI is always poor at Persian.

Keep Kubernetes kinds, YAML, chart identifiers, `values`, kubeconfig, Docker
contexts, package formats, checksums and product names recognizable. Use local
technical conventions rather than replacing identifiers with literal translations.
For Chinese, use simplified characters; do not silently route Traditional Chinese
preferences to simplified Chinese. Japanese screenshots retain real English UI.
For Portuguese, use Brazilian usage. For Spanish, preserve standard technical
terms; never imply separate regional products. For German/French, use consistent
formal address in the product website. About may use its established personal tone.

## Routes and preferences

Russian retains `/spk-ocular/`; other locales use `/spk-ocular/{code}/`.
`src/lib/languages.mjs` defines native names and routing. On the root only, saved
explicit preference wins, then the first supported browser language, then English.
Explicit localized URLs always win. Crawlers and automated audits retain their
requested page. URL query and fragment survive a JavaScript language change.
Storage is optional. If it is blocked, a manual Russian choice on the legacy
root adds `?lang=ru` to override browser detection; other choices remove that
marker and preserve unrelated query parameters. With JavaScript disabled, all localized pages and the native
`details` link menu remain usable. No runtime translation API or external fonts.

The header uses a fixed-size SVG chevron aligned with the language label;
Unicode arrow glyphs are avoided because their ink position depends on the font.
The native details/summary menu remains usable without JavaScript.

Every localized page has canonical and alternate links; `sitemap.xml` includes
all routes. Source/download URLs are locale-independent. Author and support links use the language-neutral root
`https://sipaha.github.io/about/`; the destination chooses its own language. Social images
for additional locales reuse the existing English card; they are not presented
as translated visual assets.

## Checks

`make check` includes dictionary shape, placeholder and technical-number checks,
Astro validation/build, existing RU/EN interaction tests and the additional-language
matrix. Check widths 320/375/768/1024/1440 in light/dark, accessible language menus,
localized DEB/MSI labels, native download URLs, 28px anchor clearance, real images,
theme persistence and WCAG AA. Dedicated browser cases cover language detection,
saved choices, direct routes, hash/query, storage denial and no-JavaScript switching.
Inspect screenshots in Solution scratch before publishing. Add each future locale
to both content and tests; do not advertise incomplete dictionaries.

## Released capability copy

The hero describes direct Kubernetes API access without kubectl and embedded
Helm without a separate Helm CLI beside the download action. The infrastructure
section separately states that no central management server or in-cluster Ocular
components are required. It retains the continuing need for external
authentication helpers configured in kubeconfig. The tools section
explains Ctrl+K/Command+K navigation to views, environments and namespaces, and
type/name resource queries such as `dep model` within selected namespaces.
The query capability is available in the published application;
it reads one requested kind, rather than searching every kind in the cluster.
Keep these descriptions factual, with no competitor or exclusivity claims.

Investigation copy describes the cluster graph as declared relationships/routes,
Timeline as bounded Events snapshots rather than audit history, and RBAC
provenance separately from self-only server decisions. Saved-resource comparison
excludes Secret payloads and drafts. Workspace continuity restores metadata with
explicit connection/resume and no command replay. Log downloads read retained
source history independently of viewer filters; Copy retains its filtered-buffer
behavior. Provider retention, export caps and incomplete reads are explicit.

Both RU/EN screenshot sets use actual app captures; the other six locales retain
the explicit English-UI notice. Preserve sh/readlink/tar requirements, the UTF-8
editor limit of 2 MiB, desktop-only file downloads and refusal to overwrite existing
names. All screenshot and social-image URLs carry the 1.8.0 version query.
Security uses current 1.8.0 RU/EN captures and a localized caption
identifying simulated security findings. All eight locales describe workload
configuration, declared RBAC rules, NetworkPolicy selection and existing Trivy
Operator reports without claiming live scans, exploitability or compliance.
Security neither installs the operator nor runs scans. New feature copy must
describe a verified published release; release asset verification is required before publication.

Linux installation copy describes the released 1.6.1 DEB/RPM glibc 2.35 floor.
Ubuntu 24.04 remains the native build baseline. Normal package installation and
desktop/browser startup are verified on Ubuntu 22.04 and Debian 12/13 (DEB),
and Fedora 44 (RPM) on x86-64; Ubuntu 22.04 also has native ARM64 verification.
All eight locales retain GTK 3/WebKit2GTK 4.1 requirements in browser mode and
the isolated userspace/shared-kernel/X11 scope. These checks do not certify stock
kernels, Wayland or GPU drivers. Do not generalize the matrix into universal
Linux compatibility.

The cluster-care section is complete in all eight languages. It describes
selected-kind/namespace loading, compact shared caches and watches, request/idle
retention limits, All Namespaces admission and user refresh/cancel controls. It
contains no unmeasured performance statistic or no-impact guarantee. Kubernetes
and Docker captions explicitly identify simulated API data from isolated HTTP
fixtures; Helm and Security remain explicitly synthetic.

The large-inventory section emphasizes the released resource controls and the
complete 10,000-Pod fixture result. It does not compare unpublished builds or
state that production testing is absent. The 10 requests/s value is a per-session
read budget with burst 20 and four finite-response slots, not observed throughput
or a cluster-wide cap. The combined app/Chromium PSS peak is prominent outside the conditions, with
Linux/browser-mode and measured-version scope visible. Warm LIST counts and
internal stability verdicts are omitted from sales copy; original technical
evidence remains linked. Keep these distinctions in all locales.
