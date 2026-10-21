# Ocular, Sextant, Rubick and OpsDeck

The three competitors challenge different parts of Ocular's proposition.
Sextant competes on the breadth of Kubernetes operations and diagnosis. Rubick
competes on the quality of explanations within an investigation. OpsDeck
competes for the engineer's whole working session. Ocular should evaluate each
against those jobs rather than combine unrelated features into a numerical score.

The detailed [Sextant comparison](sextant-comparison.md) contains the original
article/comment investigation. This document adds Rubick and OpsDeck and revises
the shared positioning implications. Recommendations are proposals for later
selection, not an implementation plan or authorization.

## Source and release boundaries

Inspection was performed on October 21, 2026, without running competitor
applications, build scripts or real infrastructure. Source and published
release identities were checked separately:

| Product | Inspected current source | Latest published release inspected | Release commit |
| --- | --- | --- | --- |
| Ocular | `0d3429581ad59926df2b3eb96d11ccde8a48ac70` | v1.9.0 | Current Argo/Flux additions postdate this release. |
| Sextant | `be00e44ed19f7c2f0c17de40ebdca31e3a84c1a2` | v0.21.0 | Same as inspected source. |
| Rubick | `29db352165a029f40a90b3b8e42f4ac3fc078187` | v4.21.2 | `030c6012bea9e00a934f6051add2f979f25f333d` |
| OpsDeck | `089e00305f162b7a77126d2a9194d607b3ad65b5` | v0.8.0 | `4bf5c9e36bb0d70fcd8cc1d277208971a1b7acf7` |

Rubick and OpsDeck package versions still match their latest releases, but their
branch heads do not. Package version equality is not proof that branch changes
are shipped. Release commits above were resolved from annotated remote tags.
This analysis attributes inspected code to the pinned source unless release
notes or feedback explicitly establish shipment.

The complete issue-endpoint pagination yielded:

- Rubick: 328 entries, comprising 28 issues and 300 PRs; all 28 issues closed
  at retrieval. Eight issue-author accounts, including the owner. There were
  108 issue comments, all retrieved. A large part of the useful feedback came
  from repeated outside-user reports rather than the owner alone.
- OpsDeck: 24 issues, four open, among fewer than 100 combined issue/PR entries.
  All 24 issue bodies were retrieved. Comments for 19 issues were saved as
  JSON (35 comments); the remaining five discussions were read from public
  GitHub HTML after the unauthenticated API limit was reached. Cached search
  views can show older status: the current API snapshot takes precedence.
- Sextant's 224 issues and 44 comments have a different provenance: 222 issues
  came from the owner account. Counts are not interchangeable measures of
  adoption, security or maturity.

No private security attachments, user screenshots or linked diagnostic logs
were downloaded. Repo-owned screenshots were viewed for layout only. Rubick's
image manifest dates its captures to August 11 and source `cd1834a`; they are
not evidence of the latest version's exact appearance or usability.
[Rubick screenshot provenance](https://github.com/Dudude-bit/rubick/blob/29db352165a029f40a90b3b8e42f4ac3fc078187/docs/images/MANIFEST.md).

## Rubick: explanation is the product

Rubick's strongest distinction is a coherent question-oriented investigation:
what is failing, how traffic reaches it, what the application actually checked,
and which controller may reverse an edit. This is closer to Ocular's stated
investigation direction than a simple comparison of supported resource kinds.

The inspected delivery model separates a quiet provenance mark, a contextual
warning and an action-time intervention. Ordinary managed objects should not
all grow alarm banners. Where a controller would reapply state, the action can
explain that fact and still let an operator make a legitimate incident change.
The implementation also distinguishes a claimed source from confirmed delivery.
[Delivery model](https://github.com/Dudude-bit/rubick/blob/29db352165a029f40a90b3b8e42f4ac3fc078187/src/lib/delivery.ts),
[action surface](https://github.com/Dudude-bit/rubick/blob/29db352165a029f40a90b3b8e42f4ac3fc078187/src/components/resources/delivery-intercept.tsx).

This is a concrete gap for Ocular: its current GitOps links and managed-by hint
are useful evidence, but not a complete explanation of HPA/GitOps behavior at
every relevant ordinary edit. Adding more CRD pages would not close that gap.
The explanation must remain conditional: controller settings and status do not
prove an exact future reconciliation time or that a write will be reverted.

Rubick's contextual resource page connects replicas/HPA/PDB, usage and routing.
It also connects Prometheus-compatible history and Loki for logs that survive a
Pod. Configured integrations require an explicit address; a helper can resolve
an in-cluster query Service and maintain a local forward through Pod replacement.
The integration contract prevents vendor-specific assumptions from spreading
through generic UI surfaces.
[Integration contract](https://github.com/Dudude-bit/rubick/blob/29db352165a029f40a90b3b8e42f4ac3fc078187/CONTRIBUTING.md).

The workload-first page is a stronger diagnostic composition than presenting
only independent tables. That is a source/screenshot-based design assessment,
not a timed usability result. Ocular's graph, Properties, Events and logs provide
many ingredients; the opportunity is to assemble them around a question.

Rubick also has portable investigation reports with coverage disclosure and
redaction. The 4.21.0 notes describe wider Share coverage, additional masking
and cluster-bound publish identities. A portable report can reduce handoff
friction, but masking arbitrary logs is never a universal guarantee that a
report is safe to publish. Ocular should consider reviewed local evidence
export before any shared or hosted publication feature.
[Report release notes](https://github.com/Dudude-bit/rubick/blob/29db352165a029f40a90b3b8e42f4ac3fc078187/CHANGELOG.md).

Rubick does not establish universal superiority. The current feature inventory
has no equivalent Docker/Compose provider, embedded Helm lifecycle, offline
learning laboratory or Ocular/Sextant-style scoped Kubernetes agent interface.
This is absence from inspected registration/source maps, not proof that such
features could never be added. Its documented Helm scope emphasizes release
inspection and selected operations, and it deliberately declines some adjacent
features. Its two-language scope is smaller than Ocular's eight.

The project is older than the two other inspected competitors: repository
creation was December 18, 2025, versus October 11, 2026 for Sextant and September
28, 2026 for OpsDeck. Calendar age and version number still do not establish
production maturity. Rubick discloses less live coverage of AWS/Azure paths than
its regularly used GKE/Traefik setup. The macOS distribution is described as
Developer ID-signed and notarized, with signing configuration present in the
release workflow; signature verification of a downloaded package was not done
here. Windows remains unsigned.
[Distribution/build configuration](https://github.com/Dudude-bit/rubick/blob/29db352165a029f40a90b3b8e42f4ac3fc078187/.github/workflows/release.yml),
[product scope and stated limits](https://github.com/Dudude-bit/rubick/blob/29db352165a029f40a90b3b8e42f4ac3fc078187/README.md).

## What Rubick's users teach us

| Evidence | Actual feedback and chronology | Implication for Ocular |
| --- | --- | --- |
| [#67](https://github.com/Dudude-bit/rubick/issues/67), [#106](https://github.com/Dudude-bit/rubick/issues/106), [#148](https://github.com/Dudude-bit/rubick/issues/148), closed | Windows OIDC login and renewal required multiple attempts. In #148 a user still saw failures after 4.13 and 4.14, then reported success after 4.15. | A green mock and a claimed fix are weaker evidence than a user's complete authentication/expiry/switching session. Connection recovery belongs among core acceptance scenarios. |
| [#69](https://github.com/Dudude-bit/rubick/issues/69), closed | Working overlay gateways, absent addresses/status, extension direct responses and ListenerSet parents exposed false route diagnoses. Follow-up reports disproved early “fixed” interpretations. | No address, no route condition and no backend are separate cases. Controller-specific unknowns must not become certain failure or success. Test real healthy paths, not only CRDs without controllers. |
| [#71](https://github.com/Dudude-bit/rubick/issues/71), closed | VictoriaMetrics needed configurable Service/port/subpath. Empty `kube_node_info` was wrongly treated as telemetry for another cluster; a missing volume metric was a different gap. | Prometheus-compatible does not mean identical URL shape or metric coverage. Explain absent series individually and never infer zero usage or wrong-cluster identity from an empty query alone. |
| [#68](https://github.com/Dudude-bit/rubick/issues/68), closed | Short-lived seven-day certificates were warned about almost immediately. The user pointed to renewal scheduling. | Fixed warning windows can create permanent noise. Lifetime, renewal state and operator evidence matter more than one universal “expires soon” threshold. |
| [#138](https://github.com/Dudude-bit/rubick/issues/138), [#304](https://github.com/Dudude-bit/rubick/issues/304), closed | Integration locks checked the wrong permission or scope, and newer adapters repeated the mismatch. | Discovery, CRD inspection, resource list/get and namespace scopes are distinct rights. Capability indicators must describe the request the page will actually make. |
| [#178](https://github.com/Dudude-bit/rubick/issues/178), closed | Copy names, consistent peek/full-page behavior, preserved Logs tab/filter, long-name columns and discoverable changes mattered in daily work. | Continuity and predictable gestures are product features. A user following one deployment across kinds should not repeatedly rebuild context. |
| [#208](https://github.com/Dudude-bit/rubick/issues/208), closed | Users wanted reverse workload-to-Helm navigation; investigation also found releases omitted when optional values were absent. | Bidirectional relationships matter, and real external storage often omits optional fields. Show a label as a claim until independently verified. |
| [#229](https://github.com/Dudude-bit/rubick/issues/229), closed | Wrong-cluster cached rows and an unbacked-off refused watch were recorded in an internal report. | Cache identity must include context/incarnation. A watch-init marker is not successful data delivery; persistent 403 must not become a request storm. This is historical evidence, not a reproduction of the defect in current Rubick. |
| [#70](https://github.com/Dudude-bit/rubick/issues/70), closed | A user did not want a half-hour source build; an outside maintainer supplied a binary AUR package. | Distribution friction affects adoption even when features work. Community packaging is more useful than another install command nobody has tested. |

These are more useful than counting closed issues. Several threads show both
initial failure and user retesting, allowing confidence to increase without
pretending the whole project has been production-certified.

## OpsDeck: a session-wide cockpit

OpsDeck's proposition is broader than a Kubernetes GUI. Its terminal, SSH hosts,
databases, notes/tasks, embedded web services, IDE/git, password stores and AI
context aim to reduce movement between applications. A user can prefer that
composition even when another product has deeper Kubernetes semantics.

The backend has separate modules for Kubernetes, PTY, SSH/SFTP, database tunnels,
notes, key stores, IDE, AI, alerts and embedded webviews. The frontend is
TypeScript without React. Kubernetes inspection uses kube-rs; interactive
operations also launch installed tools such as `kubectl`, while SSH/SFTP and
some cloud operations depend on their system CLIs. Embedded Grafana/Argo/GitLab
views preserve those products' full interfaces, together with their login and
webview behavior. OpsDeck also has a native Argo Applications action path: it
is inaccurate to call all its Kubernetes integration a web embed.
[Kubernetes implementation](https://github.com/LeoAlecksey/opsdeck/blob/089e00305f162b7a77126d2a9194d607b3ad65b5/src-tauri/src/k8s.rs),
[embedded views](https://github.com/LeoAlecksey/opsdeck/blob/089e00305f162b7a77126d2a9194d607b3ad65b5/src-tauri/src/embed.rs),
[database tunnels](https://github.com/LeoAlecksey/opsdeck/blob/089e00305f162b7a77126d2a9194d607b3ad65b5/src-tauri/src/dbtunnel.rs).

Its useful Kubernetes conveniences include workload-wide logs, a log-time
histogram, requests/limits beside usage, previous exit information, bulk
selection and observed Event history. This is not equivalent to Sextant's
request-metric graph, network policy analysis, right-sizing or cost adapters.
The author keeps the remaining cost/live-traffic/network-graph request open.
[User/author scope discussion](https://github.com/LeoAlecksey/opsdeck/issues/64),
[Event recorder](https://github.com/LeoAlecksey/opsdeck/blob/089e00305f162b7a77126d2a9194d607b3ad65b5/src-tauri/src/k8s_events.rs).

### AI and trust boundaries

“AI integration” means different things in these products:

- OpsDeck's MCP implementation in `ide.rs` exposes editor selections, note/file
  opening and IDE compatibility methods. It is not Sextant's cluster-tool
  permission model. A local or external AI chat suggests commands for explicit
  insertion; it does not execute them automatically. External agent CLIs in
  terminals have their own permissions and direct shell capabilities.
- Ocular mediates selected infrastructure operations through explicit grants,
  scoped plans and bounded readers, while not supplying an embedded assistant.
- Sextant offers standard MCP cluster/diagnostic tools and its own guard.
- No equivalent general-purpose cluster-agent grant layer was identified in
  Rubick's inspected feature/command maps.

[OpsDeck IDE tool list](https://github.com/LeoAlecksey/opsdeck/blob/089e00305f162b7a77126d2a9194d607b3ad65b5/src-tauri/src/ide.rs),
[chat behavior](https://github.com/LeoAlecksey/opsdeck/blob/089e00305f162b7a77126d2a9194d607b3ad65b5/src-tauri/src/aichat.rs),
[Ocular agent boundaries](agent-api.md).

It is therefore unsupported to repeat a commenter's blanket claim that OpsDeck
is unsafe simply because it offers AI. The relevant distinction is mediation:
an external CLI holding a kubeconfig is not confined by a GUI's Kubernetes
read-only switch. The switch is implemented in OpsDeck's backend mutation
commands; it is an accidental-change guard for those paths, not a system-wide
sandbox over independent terminals and third-party tools.

Static inspection identifies another concrete boundary difference. OpsDeck's
`k8s_list` serializes objects through `clean`, which removes managedFields and
the last-applied annotation, but does not mask Secret data. The Secrets view
holds that object and decodes a value on the Show click. Thus the button is a
visibility control inside the renderer, rather than a separate backend value
admission like Ocular's per-key operation. This is a data-minimization comparison,
not proof of network leakage, compromise or a reproduced live exploit.
[Backend serialization](https://github.com/LeoAlecksey/opsdeck/blob/089e00305f162b7a77126d2a9194d607b3ad65b5/src-tauri/src/k8s.rs),
[renderer disclosure path](https://github.com/LeoAlecksey/opsdeck/blob/089e00305f162b7a77126d2a9194d607b3ad65b5/src/modules/k8s.ts).

OpsDeck documents loopback authentication, isolation of embedded pages, keyring
storage, signed updates and dependency checks. Those deserve credit. It also
documents limitations: native embedded pages can absorb app shortcuts, platform
shell features differ, and a running WinBox process may expose its command-line
password to the same user. No inference is made that all credentials are logged
or sent to an external model. Optional remote AI is a separate user choice.
[Documented limits](https://github.com/LeoAlecksey/opsdeck/blob/089e00305f162b7a77126d2a9194d607b3ad65b5/README.md).

## What OpsDeck's users teach us

| Evidence | Actual feedback and latest qualification | Implication for Ocular |
| --- | --- | --- |
| [#79](https://github.com/LeoAlecksey/opsdeck/issues/79), open | A Mac user praised the concept but found parallel namespace/project work and shortcuts impractical. Several parts shipped in 0.8; configurable shortcuts and action shortcuts remain. | Breadth does not replace a coherent working session. Preserve context and standard close/save/navigation behavior across modules. |
| [#68](https://github.com/LeoAlecksey/opsdeck/issues/68), closed | Embedded WKWebView covered tabs only in windowed macOS. An early claimed fix failed; an outside contributor corrected content-area geometry. The same discussion exposed Enter activating Cancel in forms, fixed separately. | Real native geometry and keyboard focus cannot be certified by pure layout functions alone. Test windowed/fullscreen and actual user confirmation paths. |
| [#86](https://github.com/LeoAlecksey/opsdeck/issues/86), closed with later objection | A user asked to bypass OpenSSH private-key permissions. The author supplied a permission-repair button; the user declined changing their files and disputed the claimed checking behavior. | Solving a connection problem must respect ownership of external files. Explain the exact change and alternatives; a closed label does not mean acceptance. No current runtime reproduction was done here. |
| [#77](https://github.com/LeoAlecksey/opsdeck/issues/77), closed | Windows monitoring suggested reusing an SSH session even though the platform lacks the same ControlMaster behavior; the fix clarified that key login was required. | Honest platform-specific diagnostics can be the correct fix; do not promise a capability that does not exist. |
| [#76](https://github.com/LeoAlecksey/opsdeck/issues/76), closed | Windows canonical path prefixes broke vault membership checks; the author reports fixes in 0.7.1 and later multiselect. | Validate safety checks with real platform paths. A check that always refuses valid input is still a product defect. |
| [#71](https://github.com/LeoAlecksey/opsdeck/issues/71), [#72](https://github.com/LeoAlecksey/opsdeck/issues/72), [#73](https://github.com/LeoAlecksey/opsdeck/issues/73), [#75](https://github.com/LeoAlecksey/opsdeck/issues/75), closed | Visible-row bulk selection, ownership-based Pod navigation, log histogram and export were concrete user requests. | Small repeated investigations can be more valuable than new modules. Preserve filters, source attribution and independent bulk outcomes. |
| [#60](https://github.com/LeoAlecksey/opsdeck/issues/60), open | A theme control changed only terminal colors, while the user expected the whole application. | Label a preference by its actual scope. Do not infer an app-wide light theme from a terminal palette picker. |
| [#50](https://github.com/LeoAlecksey/opsdeck/issues/50), open | Users requested automatic repository-based updates rather than manual downloads. | Distribution and update behavior are part of everyday usability, independently of the internal updater. |

The developer has received useful outside contributions and real platform
reports. That is stronger evidence of some feedback loops than a repository's
star count, while still not a measure of retention or production reliability.
Its CI defines Rust checks, synthetic browser tests and desktop e2e jobs; these
were inspected, not rerun or treated as certification of every live integration.
[Build/test workflow](https://github.com/LeoAlecksey/opsdeck/blob/089e00305f162b7a77126d2a9194d607b3ad65b5/.github/workflows/build.yml).

## Shared comparison and decision criteria

| User job or constraint | Ocular | Sextant | Rubick | OpsDeck |
| --- | --- | --- | --- | --- |
| Kubernetes operation breadth | Substantial core, selected workspaces | Widest inspected specialized coverage | Focused semantic integrations | Core Kubernetes inside a much broader cockpit |
| Explain a request's path | Graph/objects; less integrated diagnosis | Broad configuration and telemetry diagnosis | Strong workload/route explanation | More basic resource relations and checks |
| Historical observability | Current metrics; separate future adapters | History, right-sizing, traffic/cost adapters | Prometheus-compatible history and Loki | Metrics/history conveniences; broader monitoring panels |
| Local Docker/Compose and Kubernetes | Native provider combination | No corresponding provider found | No corresponding provider found | Adjacent host tools; not an equivalent Docker/Compose provider |
| Helm without external executable | Embedded lifecycle and several storage drivers | External Helm required for changes | Selected release operations; external Helm paths present | Secret-based inspection and CLI-dependent actions |
| Standard cluster MCP | Custom socket/pipe API; adapter gap | Direct supported workflow | No equivalent found | IDE MCP has a different purpose |
| Local learning without a cluster | Six modules and 37-type help | Operational explanation | Operational explanation | Command assistance and working tools |
| Full DevOps desktop | Selected infrastructure workspace | Kubernetes operations console | Kubernetes investigation client | SSH, DB, IDE, notes, credentials and AI session |
| Report/handoff and parallel context | Pins/layouts and pair comparison | Views/workspaces, namespace comparison | Reports/share and context-carrying tabs | Projects, terminal/module tabs and notes |
| Installer/update convenience | Packaged six-platform contract; no in-app update | Signed updater, package managers, AppImage | Signed updater; stated notarized macOS, community packages | Signed updater and multi-platform packages; installer signing differs |

These are source/workflow assessments, not a common performance or task-time
benchmark. No ordinal product score is derived from the table.

The most credible direction for Ocular is a local infrastructure investigation
workspace spanning Docker/Compose and Kubernetes, with explicit evidence,
reviewed actions and a practical learning entry. Three qualifications matter:

1. **Cautious unknown states are not unique.** Rubick makes them a central
   proposition, and Sextant models them in several diagnostic engines. Ocular
   must demonstrate correctness in difficult cases rather than advertise the word.
2. **Free/native/local/no account is now a shared baseline.** It helps a user
   switch from a paid client but no longer explains why to choose Ocular among
   these alternatives.
3. **An extra page is easy to imitate; a reliable session is harder.** Login and
   renewal, correct namespace identity, reconnect, read denial, preserved drafts,
   scoped logs, backpressure, native menus and safe changes deserve investment
   before indiscriminate feature parity.

Recommended evaluation priorities are consistent with the Sextant assessment:
first operational trust/onboarding and evidence-backed platform testing; then
connected Service/Ingress/Gateway diagnosis and edit-time controller explanation;
then historical observability/reliability, reviewed evidence handoff and standard
agent-client connectivity. Choose backup/operator adapters from actual user
scenarios. Keep offline learning and the cross-provider workflow distinct from
claims about live production diagnosis.

Do not respond to OpsDeck by automatically building a password manager, database
IDE or full SSH workstation. Do not respond to Sextant by opening every operator
backlog. Do not respond to Rubick by merely adding warning prose: its value lies
in evidence, limits, contextual navigation and user retesting. All three offer
useful design and acceptance lessons even where Ocular keeps a narrower scope.
