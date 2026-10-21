# SPK Ocular website

Static Astro website on the independent `pages` branch. The application source
lives on `master`. The site supports eight languages and light/dark themes.

Install the pinned dependencies with pnpm and run `make check` before committing.
For local preview, use `pnpm dev`. Keep test output and TMPDIR under the shared
solution `.tmp/` with `OCULAR_SITE_SCRATCH` set to an isolated directory.

Detailed maintenance and localization guides are in the private documentation
repository. See [AGENTS.md](AGENTS.md) for the contributor entry point.
Pushing `pages` can publish the website through GitHub Actions; publishing
requires explicit owner authorization.
