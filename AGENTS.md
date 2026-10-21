# Contributor and agent entry point

Detailed project documentation and contributor rules live in the private
`internal-docs` repository, under `spk-ocular/`. In this solution, read
`../internal-docs/spk-ocular/AGENTS.md` and the applicable guides before
substantive work. Source comments referring to `docs/...` mean that private
project documentation tree. If it is unavailable, report that limitation.

- Work within the owner's selected scope. Backlog text does not authorize
  implementation. Do not delegate to subagents without explicit authorization.
- Preserve unrelated work. Never restart or kill the user's application or
  disturb shared displays, configuration, infrastructure or processes.
- Run source/build/test/git commands from this member repository. Put all
  scratch files, logs, profiles and TMPDIR under the solution's `.tmp/`.
- Test only owned isolated profiles and fixtures. No real infrastructure
  mutations without explicit scope and verified target identity.
- Both commit author and committer must be Pavel Simonov
  <sipahabk@gmail.com>, set per command; never change global Git configuration.
- Scoped commits and ordinary pushes have standing owner authorization,
  including automatic website deployment from `pages`; do not reconfirm them.
  Application releases/tags, manual publication workflows and published-history
  rewrites require explicit owner instructions.
- `make check` is required before committing: lint, tests, documentation and
  both builds. Use `VITEST_MAX_WORKERS=2` here. Do not relax assertions, budgets
  or timeouts. Inspect actual screenshots for user-visible interface changes.
- Keep code/comments and documentation English and all eight UI locales
  complete. Keep the private documentation current when behavior changes.
- Keep internal guides, plans, research and evidence out of the public source
  tree and distribution payloads. Public metadata is limited to this entry
  point, README, changelog, licenses and third-party notices. Preserve required
  third-party documentation with vendored code.

Public CI and packaging must work without private repository access. Run
`python3 scripts/check-docs.py` in `internal-docs` separately when its documents
change. Do not upload private documentation to the work knowledge service.
