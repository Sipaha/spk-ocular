# Development and verification

## Toolchain

Use Go 1.26.8 as declared by `go.mod`, Node.js 22 and pnpm 10.33.0.
Linux builds require a C compiler, pkg-config, GTK 3, WebKit2GTK 4.1 and libsoup 3
development packages. The full check also uses golangci-lint v2.11.4,
actionlint v1.7.12, Python 3, desktop-file-utils, and Playwright Chromium.
Packaging additionally uses nfpm v2.41.0. Tool versions are pinned in CI.

Run commands from the repository, not from a containing multi-project solution.
Keep temporary files in an isolated scratch directory. In the shared solution,
use its `.agents/tmp`; in a standalone checkout, a gitignored local `.agents/tmp`
can be used instead.

```sh
# Shared solution example; use an absolute, canonical path.
export OCULAR_SCRATCH_DIR="$(realpath ../.agents/tmp)"
mkdir -p "$OCULAR_SCRATCH_DIR/go"
export TMPDIR="$OCULAR_SCRATCH_DIR/go"
export VITEST_MAX_WORKERS=2
pnpm --dir web install --frozen-lockfile
pnpm --dir tests/e2e install --frozen-lockfile
pnpm --dir tests/e2e exec playwright install chromium
make check
```

Unset HTTP_PROXY, HTTPS_PROXY, ALL_PROXY and their lowercase variants when they
would intercept loopback or unreachable test-cluster addresses. Playwright's
isolated application environment also clears connection-related variables.
Its parent runner still inherits proxy settings: when keeping a proxy, include
literal `localhost,127.0.0.1,::1` entries in both `NO_PROXY` and `no_proxy`.
Playwright's server-readiness probe does not interpret CIDR entries such as
`127.0.0.0/8` as a loopback exemption.

## Commands

| Command | Purpose |
| --- | --- |
| `make build` | Web bundle and static browser-mode binary in `build/bin/spk-ocular` |
| `make build-desktop` | Native development window in `build/bin/spk-ocular-desktop` |
| `make licenses` | Regenerate committed third-party notices from exact build inputs |
| `make release VERSION=1.0.0` | Native production binary without DevTools |
| `make package-linux` | Production DEB/RPM and desktop/browser archives |
| `make lint` | Go vet and lint with both tag sets, ESLint/TypeScript, workflow/shell validation |
| `make test` | Go race, web, browser e2e and packaging/documentation contract tests |
| `make check` | Full gate: lint, tests and both development builds |
| `make test-packaging` | Release input/archive tests, documentation links/language, desktop entry |
| `make pss PID=123` | Application and webview memory accounting |

`internal/appfiles/icons/icon.svg` is the canonical Ocular eye/lens mark, matching
`public/icon.svg` in the independent website branch. Native PNG sizes and the
browser favicon are generated from this SVG, without drawing separate variants.
Run `scripts/render-icons.mjs` with `OCULAR_SCRATCH_DIR` and `TMPDIR` set to solution
scratch. It supersamples each native size at 4x and downsamples it, then copies
the SVG to `web/public/icon.svg`. The website copy must be kept byte-identical
when the mark changes. Packaging derives Windows/macOS icon formats from the
same native PNG. Published release assets change only with a new release.

GTK supplies the 16/24/32/48/64/128 px family; the 256 px PNG remains the
application and packaging fallback. The renderer needs e2e Chromium and uses
Catmull-Rom for the final native sizes.

`make run` and `make run-browser` use normal user configuration. Automated or
manual verification must launch the built executable with isolated HOME,
DOCKER_CONFIG, SPK_OCULAR_HOME and KUBECONFIG instead. Never restart a user's
running app to verify a change. Development desktop builds are replaced atomically.

## Test environments

Browser e2e uses three configurations under `tests/e2e`: target discovery,
synthetic-provider interactions, and restarts on the same data directory.
The synthetic provider supplies live logs, echo terminals, and local HTTP tunnels;
it requires `--test-api --test-synthetic`. Tests import `test` from `fixtures.ts`
so each starts with a clean persisted page snapshot. Lifecycle-owning restart
tests use Playwright directly and own their application's shutdown.
Staged loading fixtures keep every rows response in the current phase until
the test explicitly advances it. Exercise an event-driven resync while checking
partial loading; a single modified response can be replaced by a background pull.

`OCULAR_SCRATCH_DIR` selects e2e scratch/output directories; its default is the
parent solution's `.agents/tmp`. `E2E_BIN`, `E2E_PORT` and `E2E_SYNTH_PORT` select
the browser binary and ports. Check port availability and choose another free
port instead of killing a process belonging to another task.

Fake Engine event emission queues a server-side message; it does not acknowledge
the client's reader. Tests for initial reconciliation hold the list response
until the feed has recorded the event, so a late live event cannot be mistaken
for work observed during the initial snapshot.

Go tests exercise fake Kubernetes/Engine servers, scope unions, stream lifecycles,
action writes, secret masking, and persistence. Tests never require a real cluster
for `make check`. Synthetic browser tests exclude Chromium’s `--hide-scrollbars`
launch flag so screenshots and scrollbar drag checks exercise visible controls. Explicit integration targets fail if their fixture is absent:

- `make kind-up`, `make test-kind`, `make e2e-kind`, `make kind-down`: disposable
  kind-ocular-dev, with kubeconfig in `build/kind-ocular-dev.kubeconfig`.
  RBAC and metrics fixtures are in `scripts/kind-{seed,rbac,metrics}.sh`.
- `make dind-up`, `make test-dind`, `make e2e-dind`, `make dind-down`: the isolated
  `ocular-dind` Engine. `scripts/dind-verify.sh` checks recorded identity,
  `ocular.test=dind`, loopback port and daemon hostname before mutations.

Never run fixture mutations against the user's clusters or normal Docker Engine.
The DIND container's `/run` is tmpfs so stale daemon pid files do not survive stops.

`TestDindStandaloneLifecycle` covers an owned container without Compose labels:
watch events, project isolation, details, logs, exec, metrics, stale reviews and
reviewed start/stop/restart/delete. `TestDindStandaloneDoesNotInheritProjectGrants`
checks the real agent socket without expanding existing project grants. The
browser DIND suite also exercises standalone rows and scope transitions. These
tests use the same verified `ocular-dind` endpoint and clean up their own IDs.

## Actual desktop verification

Use a dedicated X display and an isolated profile. Preserve displays/processes
owned by another task. The test-only `scripts/xinput.py` drives XTest clicks,
keys, resizing, scrolling, and WM_DELETE_WINDOW. `xwininfo -root -tree` identifies
the current window; old PID/window files are not proof of ownership.

Take and inspect actual screenshots with ImageMagick `import`, stored in scratch.
Check the Russian locale with LANGUAGE=ru, including keyboard behavior. LANG
alone does not override LANGUAGE. The app's test API writes its private loopback
address/token to the isolated data directory when launched with `--test-api`.
Do not include that token in documentation or uploaded diagnostics.

The embedded icon is a 256×256 PNG. Check `_NET_WM_ICON`, not just WM_HINTS.
GTK overlay scrollbars intercept a wider region than the visible thumb: retain
the 24 px resource-list inset and verify the entire star button is clickable.
Native selects are not used because GTK popup styling differs from the app.

## Performance verification

Use `scripts/kind-load.sh`, `scripts/kind-churn.sh`, and `scripts/soak-sample.sh`
only with the disposable fixture. `scripts/soak-verdict.py` evaluates bounded
growth after warm-up and settling after load. Samples include app/webview child
processes and available host memory; do not compare samples under eviction.
Use the existing performance limits and timeouts. Fix a regression rather than
loosening assertions. Keep xterm and CodeMirror lazy, virtual row IDs stable,
and resize DOM updates limited to one per animation frame.

## Release artifacts

`make package-linux RELEASE_VERSION=1.0.0 ARCH=amd64` builds and packages on the
matching native Linux host. It does not install, tag, push, or publish anything.
Inspect the generated packages with:

```sh
python3 packaging/verify.py --version 1.0.0 --arch amd64
```

The verifier checks actual archive/DEB/RPM contents, binary version and machine
architecture, permissions, dependencies, menu entry, icons, license and hashes.
CI also installs and removes the DEB on its disposable runner. Local verification
extracts packages into scratch and must not install over the user's application.


Windows/macOS packages use matching native hosts:

```sh
python packaging/portable.py --version 1.0.0 --os windows --arch amd64
python packaging/verify-portable.py --version 1.0.0 --os windows --arch amd64
# On macOS use --os darwin; either platform also accepts --arch arm64.
```

Windows requires WebView2 and WiX 5.0.2 (`dotnet tool install wix --version 5.0.2`).
macOS requires Xcode Command Line Tools. The builder creates Windows icon/manifest
resources and the macOS app bundle from the existing application icon. Signing
occurs after bundle assembly and before DMG creation. The native CI jobs exercise
platform transport/permissions, inspect artifacts, and start an isolated native
app with synthetic data, saving screenshots. They never use a developer profile.
The Windows smoke driver uses PowerShell 7 (`pwsh.exe`) for window capture and
shutdown. Capture checks the window's process owner and renders that HWND directly,
so another desktop window cannot replace the application in the screenshot. The
capture has a 30-second limit and reports its stages in the CI log.
Windows desktop diagnostics go to `SPK_OCULAR_HOME/desktop.log` (replaced on launch).

## Helm verification

`internal/helm` tests execute the embedded SDK lifecycle against fake Kubernetes
storage/client boundaries, HTTP chart repositories and a private TLS OCI registry.
They cover exact-version/digest binding, cancellation, no credential forwarding
across repository redirects, stale/one-shot plans and secret-free settings reads.
`internal/helm/sqlstore` tests exercise cancellation, pool cleanup, denied reads
and failed commits with a SQL protocol mock. Real database and Kubernetes
coverage is provided by the explicit integration target below.
The synthetic provider exposes a disposable in-memory Helm release and SDK
operations for browser/native verification. It cannot mutate an external cluster.
`tests/e2e/helm.spec.ts` builds a local chart repository in solution scratch and
exercises catalogue → install → values upgrade → rollback, plus reviewed removal,
private settings and compact layout. No normal user configuration is used.

### Real Helm and PostgreSQL integration

```sh
OCULAR_KIND_KUBECONFIG=/absolute/path/to/disposable-kind.kubeconfig make test-helm-live
```

The runner (`scripts/helm-live.py`) requires a single `kind-ocular-dev` context,
a loopback API endpoint and nodes whose provider IDs identify `ocular-dev`.
It creates a private profile under `OCULAR_SCRATCH_DIR` (solution `.agents/tmp`
by default), copies the kubeconfig there and isolates HOME, DOCKER_CONFIG,
SPK_OCULAR_HOME, temporary files and caches. No user application is launched or
restarted. Kubectl, Go, a running disposable kind cluster and access to the
fixture images are required; no external Helm CLI or existing database is needed.

PostgreSQL 17.6 Alpine runs in an owned temporary namespace with ephemeral
storage and a generated password. An owned loopback port-forward supplies a
private connection file to the tests; the database carries a disposable-fixture
marker. Cleanup removes that namespace, this run's labeled CRDs, the forward and
the connection file even after test failure. Do not interrupt cleanup with SIGKILL.

The target runs race-enabled tests through the production Kubernetes session
adapter and embedded Helm SDK, using local HTTP chart archives. It covers Secret,
ConfigMap and SQL storage; preview without writes; actual readiness and hooks;
upgrade, stale review rejection, failed-hook diagnosis, rollback and uninstall
with retained history; CRD creation; RBAC denial; timeout, user cancellation and
session-close cancellation. A transport fault drops the response after a real
API write to check uncertain outcome reporting and absence of mutation replay.
It does not disconnect or modify the cluster network.

Database tests cover upstream Helm schema/codec interoperability, migration reuse,
namespace isolation, custom-label persistence, RLS/permission errors, blocked-query
cancellation, pool closure, real deferred commit failure and termination of an
owned backend connection. These fixtures establish integration behavior for this
setup, not compatibility with every cluster admission policy, database deployment
or chart. Keep run output in scratch, not in maintained documentation.

Ordinary unit/race runs must unset `OCULAR_KIND_KUBECONFIG`,
`OCULAR_KIND_RBAC_DIR`, `OCULAR_DIND_HOST` and `OCULAR_HELM_LIVE`; these variables
opt into integration suites and must not leak from a fixture shell.

## Configuration import and encryption checks

`configurations.spec.ts` uses its own disposable process and profile to exercise
unchecked first-run imports, selective file registration, encrypted YAML creation,
restart/locked state, incorrect-password refusal and unlock. The other browser
resource suites explicitly import their fixture files through the UI API before
running; they do not bypass the production registry. Provider tests verify
persistent links, fresh nonces, record-bound ciphertext, absence of plaintext
credentials/passwords and stale-writer refusal. No real kubeconfig is a test input.

## Localization checks

`web/src/i18n.test.ts` checks every locale against English UI keys and the Go
provider message catalogues, including exact placeholder multiplicity. The
browser language suite exercises all eight choices, native-name menus, live
workspace preservation and denied storage. The lifecycle suite checks a saved
language after restarting its isolated process. See [localization](localization.md)
for system/browser precedence and translation limits.


For local Playwright fixtures, clear HTTP/HTTPS proxy variables in the test
command if the runner's inherited proxy intercepts loopback readiness checks;
this is a per-command test setting, not a change to user/global proxy settings.

Tool interaction regressions cover prefetch, cancellation, single-Pod setup,
concrete-container choices, read-only revision comparisons and relation navigation.
Terminal tests check physical Cyrillic keydown/keypress pairs and one resize on
drag release. A real BusyBox PTY fixture distinguishes shell SIGWINCH prompt
redraws from synthetic terminal size reports; the app must never filter these
shell bytes to conceal a redraw. Window actions are accessible icon buttons at
the right edge of the log toolbar.

Frontend lint/build reject case-insensitive module-path collisions, including
identically named .ts/.tsx modules. Model and component names must remain
distinct on Windows/macOS, not only on Linux. Playwright palette tests wait for
route handlers to settle before response disposal during page teardown.
