# Development and verification

## Toolchain

Use the Go version declared by `go.mod`, Node.js 22 and pnpm 10.33.0.
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
| `make release VERSION=0.1.0` | Native production binary without DevTools |
| `make package-linux` | Production DEB/RPM and desktop/browser archives |
| `make lint` | Go vet and lint with both tag sets, ESLint/TypeScript, workflow/shell validation |
| `make test` | Go race, web, browser e2e and packaging/documentation contract tests |
| `make check` | Full gate: lint, tests and both development builds |
| `make test-packaging` | Release input/archive tests, documentation links/language, desktop entry |
| `make pss PID=123` | Application and webview memory accounting |

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

`OCULAR_SCRATCH_DIR` selects e2e scratch/output directories; its default is the
parent solution's `.agents/tmp`. `E2E_BIN`, `E2E_PORT` and `E2E_SYNTH_PORT` select
the browser binary and ports. Check port availability and choose another free
port instead of killing a process belonging to another task.

Go tests exercise fake Kubernetes/Engine servers, scope unions, stream lifecycles,
action writes, secret masking, and persistence. Tests never require a real cluster
for `make check`. Explicit integration targets fail if their fixture is absent:

- `make kind-up`, `make test-kind`, `make e2e-kind`, `make kind-down`: disposable
  kind-ocular-dev, with kubeconfig in `build/kind-ocular-dev.kubeconfig`.
  RBAC and metrics fixtures are in `scripts/kind-{seed,rbac,metrics}.sh`.
- `make dind-up`, `make test-dind`, `make e2e-dind`, `make dind-down`: the isolated
  `ocular-dind` Engine. `scripts/dind-verify.sh` checks recorded identity,
  `ocular.test=dind`, loopback port and daemon hostname before mutations.

Never run fixture mutations against the user's clusters or normal Docker Engine.
The DIND container's `/run` is tmpfs so stale daemon pid files do not survive stops.

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

`make package-linux RELEASE_VERSION=0.1.0 ARCH=amd64` builds and packages on the
matching native Linux host. It does not install, tag, push, or publish anything.
Inspect the generated packages with:

```sh
python3 packaging/verify.py --version 0.1.0 --arch amd64
```

The verifier checks actual archive/DEB/RPM contents, binary version and machine
architecture, permissions, dependencies, menu entry, icons, license and hashes.
CI also installs and removes the DEB on its disposable runner. Local verification
extracts packages into scratch and must not install over the user's application.
