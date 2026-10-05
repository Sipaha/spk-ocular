# SPK Ocular website

This orphan `pages` branch contains the static product website, independently of
the application on `master`. Keep both worktrees intact. The website uses Astro,
plain TypeScript and CSS, Russian and English, and light/dark themes.

Run `make check` before committing: pure logic tests, Astro checks/build, and real
browser verification. Inspect desktop/mobile screenshots in both themes. Put all
test output, browser profiles, logs and temporary downloads in the solution's
`.agents/tmp/site`; set `OCULAR_SITE_SCRATCH` in another workspace. No `/tmp`.

Use actual product screenshots and verified product facts. Do not invent release
assets, downloads, statistics, testimonials or platform support. Download links
must survive missing releases, API failure and JavaScript being disabled.

Documentation and code comments are English; product copy is localized.
New author and committer: Pavel Simonov <sipahabk@gmail.com>. No global Git changes,
subagents, app restarts, infrastructure mutations, release tags or history rewrites.
