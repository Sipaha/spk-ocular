# SPK Ocular: contributor and agent guide

SPK Ocular is a local infrastructure viewer for Kubernetes and Docker Compose.
The shared Go API serves a Wails desktop window or a loopback browser UI.
Release platforms are Linux, Windows and macOS, each on amd64 and arm64.
Linux uses GTK 3/WebKit2GTK 4.1; Windows uses WebView2; macOS uses WKWebView.

## Read first

- [README](README.md): installation, commands, supported platforms.
- [Usage](docs/usage.md): current user-visible behavior.
- [Architecture](docs/architecture.md): module boundaries and invariants.
- [Agent API](docs/agent-api.md): local automation permissions and transport.
- [Development](docs/development.md): toolchain, checks and isolated fixtures.
- [Releases](docs/releases.md): packages, workflows, versioning and publication.
- [Backlog](docs/backlog.md): future work; it is not permission to start a task.

## Documentation policy

Documentation describes **what exists now or explicitly planned work**. Do not
turn it into a historical archive. Update the authoritative section when behavior
changes; delete obsolete claims, completed implementation plans, old review
reports, superseded alternatives, session transcripts and dated test summaries.
Retain a decision's current constraint and rationale, not the story of how it
was reached. Put transient logs, screenshots, benchmarks and handoff notes in
scratch, outside product documentation. Git and GitHub Releases retain history.

All documentation and code comments are English. Russian UI text belongs in
localization data (`web/src/i18n.ts`); release descriptions may be bilingual.
Release notes live in `changelog/<version>/<locale>.md`, as in the launcher;
GitHub uses `en.md`. Keep published version notes as release metadata. Verify documentation
against source, commands and tests; do not claim a platform or workflow has run when it
has only been configured. Keep local links valid (`scripts/check-docs.py`).

## Work safely

- Preserve existing work and stay within the user's agreed task. Do not start
  backlog items or delegate to subagents without explicit authorization.
- Work inside the current solution. Build/test/git commands run from a member
  repository. Other clones and user/system configuration are read-only unless
  the user authorizes the exact path and change.
- Scratch files, logs, profiles, screenshots and test tool installations belong
  in the solution's `.agents/tmp`. Set a canonical TMPDIR there. No `/tmp` scratch.
- Never restart or kill the user's application. Test launches isolate HOME,
  DOCKER_CONFIG, SPK_OCULAR_HOME and KUBECONFIG. Discover current PID/window
  ownership; stale helper files are not evidence. Leave shared X displays running.
- Only disposable kind-ocular-dev and ocular-dind are infrastructure mutation
  destinations. Verify the fixture's identity; never use the user's containers
  or real clusters. `scripts/dind-verify.sh` is mandatory before DIND mutations.
- Every new commit's author **and** committer must be
  `Pavel Simonov <sipahabk@gmail.com>`. Do not change global Git identity.
  Do not commit, push or publish outside the user's authorization.
- Published history is rewritten only on an explicit request. Before rewriting,
  save a bundle and preserve the working tree inside solution scratch; compare
  tree contents afterward. A requested remote replacement uses an exact
  force-with-lease expectation, never an unconditional force push.
- Quality is more important than speed. Run real checks, inspect actual UI
  screenshots for visible changes, and report concrete evidence and limits.
  Do not relax assertions, budgets or timeouts to make a check pass.

## Build and verify

`make build` builds web + browser binary; `make build-desktop` builds the native
window; `make release` adds production mode. The desktop development executable
is replaced atomically. A web/browser build does not update an already running
native process or its embedded assets.

`make check` is required before a commit/release: Go vet and golangci with/without
`wails gtk3`, ESLint/TypeScript, actionlint, Go race, Vitest, Playwright, packaging
contract checks, documentation validation, and both builds. Use
VITEST_MAX_WORKERS=2 on this workspace. The existing TanStack React Compiler
warning is not a failed build.

`make package-linux RELEASE_VERSION=0.1.0 ARCH=amd64` produces native packages
and archives. `packaging/verify.py` validates their real contents. Packaging
requires a native host of the requested architecture; never relabel a binary.
Windows/macOS use `packaging/portable.py` on native hosts and
`packaging/verify-portable.py`; Windows adds real MSI install/remove checks.
Published assets must include all six OS/architecture pairs and every checksum. Release
metadata, package identity and artifact names use SPK Ocular exclusively.

Tests use the synthetic provider and fake servers unless an explicit kind/DIND
integration target is requested. Fixture ports must be free; choose another port
instead of killing another task's process. `OCULAR_SCRATCH_DIR` selects e2e output.
Tests importing `fixtures.ts` reset the persisted page snapshot between specs.
Lifecycle tests own their app's shutdown and wait for it to exit.

For GTK screenshots use a dedicated/shared test display without disturbing its
owner, `xwininfo` to find the actual window, `scripts/xinput.py` for input and
`import` for captures. Inspect the image. Use LANGUAGE=ru to check localization;
LANG alone can be overridden. A window can take time to appear on a private
D-Bus without portals; don't assume a timeout means the application is broken.

## Implementation map

- API: `internal/api/api.go`, service files, `transport/http.go`,
  `transport/wails.go`, `web/src/api/client.ts`. New methods need all layers.
- Core/provider types: `internal/core`, `internal/provider`; keep generic UI
  independent of Kubernetes pod/namespace/container assumptions.
- Live data: `internal/events`, `internal/views`, Kubernetes cache/discovery/schema,
  Compose feeds, `web/src/views/viewSync.ts` and hooks.
- Workspace/navigation: `web/src/components/Workspace.tsx`, `Sidebar.tsx`,
  `ResourceDrawer.tsx`, `ResourceTable.tsx`, `ScopeSelect.tsx`, `Select.tsx`.
- Persistence: SQLite `internal/store`; `pageMemo*`, `navigationPersist.ts`,
  `columnWidths.ts`, global preferences in `web/src/store.ts`.
- Mutations: `internal/api` and provider action/edit/value modules;
  frontend `actions`, `edit`, `values` and `mayLeave` guards.
- Logs/terminal/tunnels: `internal/streams`, `internal/forwards`;
  frontend `logs`, `term`, `tunnels` and `dock`.
- Agent access: `internal/agentgrant`, `internal/agentapi`, `web/src/agents`.
- Packaging: `packaging/`, `.github/workflows/`, `VERSION` (development base), `changelog/<version>/<locale>.md`.

## Behavior invariants

### Data, ownership and permissions

Target identity is stable across unrelated configuration changes. Credentials
stay out of DTOs/logs/events/SQLite. Configuration revisions are HMACs. Views
belong to one session incarnation; responses, timers and late deliveries cannot
mutate a replacement. UID distinguishes same-name resource replacements.

Initial readiness follows delivered data, not merely an established watch.
Permission failures and partial coverage are visible, never empty success.
Discovered kinds disappear only after a successful catalog response proves it;
unconfirmed sources retain their last known kinds. No network I/O in provider
Open/Kinds; discovery is asynchronous and single-flight.

Explicit namespace sets use per-namespace reads/watches/schema probes/metrics.
Empty sets remain empty; All is explicit. Source Reset is local to that source,
and shared rows remain while any owner has them. Unscoped reads run once.
Agent grants remain independent and are checked again at the moment of writing.

One agent `logs` grant covers history, time ranges, grep, bounded live reads and
file export within its scope. Time ranges require an explicit matching-line
limit; streams always require one. Bound complete encoded API output and stream
duration, not just text lengths. Apply filters before output limits and report
every truncation. Large analysis belongs in a private Downloads export whose API
response contains metadata only. Revocation stops active reads and removes an
unreturned partial export. Do not use the UI's tail/merge limits for archive reads.

### Workspace and interaction

Namespace row/text/Enter selects exactly one and closes. Checkbox and Space in
the list toggle a set without closing; Space in search types normally. Picker
query/cursor/scroll/focus survive keyed page remounts. Close popups before a
mayLeave question. Selection is per target; Favorites and section expansion are
global. Neither unavailable catalog entries nor target switches erase them.

A favorite exists in only one navigation section, including search. Removing it
returns the original position. D&D changes order without navigation and has a
keyboard equivalent. Empty original sections disappear and counts reflect their
remaining kinds. Keep one active nav row and F6 entry point.

Collapsed groups retain their current kind. Search expands matches temporarily
without persisting that expansion. Palette object results use the selected
scope even on unscoped tables; opening a result changes the kind and reveals the
exact Ref+UID row once. Scope selection and unsaved-editor protection survive it.

Target writes share a serialized queue across remounts and await failures as well
as successes. Global optimistic changes replay pending operations after a failed
write. Persistence errors are visible. Respect the 4096-byte target-state limit.
Column widths are keyed by ID, outside the page snapshot's size budget.

Resize pointer capture/cancel/lost-capture must be cleaned up; Escape restores the
old size. Use CSS variables and one DOM write per animation frame; commit React
state on release. Resize cannot sort or select a row. Virtualizer keys are stable.

Text and resource-page titles are 14 px (titles semibold), root rem 17 px,
rows 32 px, app/resource headers 40 px, radii 2/4 px.
Keep the embedded PNG at 256 px and verify `_NET_WM_ICON` on GTK. Keep the 24 px
resource-list right inset: invisible GTK overlay scrollbar hit regions cover star
buttons without it. `@theme static` retains runtime ANSI/xterm colors. Modal
connection information preserves the table/editor, traps focus and restores it
on close, Escape and backdrop. No Overview page.

### Mutations and secrets

Use Prepare/Run with exact UID, configuration revision, route and version/Expect.
Disable HTTP mutation retries; an ambiguous result is unknown. Retry a failed
precondition only after proving no write occurred. A new preview is not implicit
permission to write. Destructive dialogs display the actual object and start on
Cancel. Bulk writes preserve independent outcomes and support stopping new work.

Secret YAML changes only metadata. Never write masked values or expose Secret
server-error details. Actual value reads/writes use the dedicated per-key path
and version guards. Values stay out of caches, history, grants and journals.
Late reveal/copy responses cannot display data after target/object/version changes.
Clipboard writes are serialized and reported successful only after completion.

Every navigation that discards edits uses mayLeave; automatic page replacement
waits for held edits. A cancelled transition preserves the prior kind, scope,
selection and editor. YAML and Secret value editors can hold guards simultaneously.

### Streams and processes

Stream IDs are one-shot and session-owned. Transport guards run before consuming
IDs. Logs are bounded by lines/bytes/frame size and explicitly report gaps.
Terminal input/output use credits; interrupt preempts queued input. Closing tabs
hangs up the in-container command before disconnecting. Protocol errors are fatal.

Never send streamed data through wails asset URLs. Keep quiet-stream nudges for
WebKitGTK. Tunnels bind loopback, recheck failed Service backends and have bounded
handshakes. Existing terminal/tunnel connections keep their original configuration.
Regex search remains in a time-limited Worker. Untrusted terminal output cannot
write the clipboard or open links.

## Test pitfalls

- kubeconfig is YAML 1.1: quote names such as `yes`, `on`, `n` in fixtures.
- Fake dynamic clients do not reproduce all field selectors, PodMetrics, server
  status bodies or watch-list behavior; use kind when those properties matter.
- Client-go WatchList readiness requires all initial deliveries, not a List call.
- Fake client reactors run under its lock: mutate the tracker, not the client.
- CodeMirror renders visible lines only. E2E must scroll before checking distant
  text. jsdom needs layout/scroll stubs and cannot establish GTK hit-test correctness.
- Native select menus are avoided. Custom popups must be focusable while positioning;
  use opacity rather than visibility:hidden. Only relevant ancestor scroll closes them.
- Option descendants' title/aria-label can alter accessible names; put tooltips on
  the row rather than decorative children. Focus and pointer menu highlight agree.
- Resource detail events are another resource grid; target the primary grid explicitly.
- CPU/RAM metrics require the cached object's current UID and sample incarnation.
- Docker log timestamps are not a total order; preserve anchor-based replay logic.
- `t.Context()` is already cancelled in cleanup. Use a fresh bounded context where needed.
- Long TMPDIR paths can exceed Unix socket limits; use the existing short-socket helpers.
- Xvfb keyboard layout resets when its last client exits; configure it with a live window.
