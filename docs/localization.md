# Application localization

The application supports Russian, English, Simplified Chinese, Spanish, German,
French, Brazilian Portuguese and Japanese. The header language menu displays
native language names and offers **Automatic**. The choice is stored in the
Ocular profile's SQLite `ui_prefs.language`, shared by desktop and browser mode.
It survives restarts and does not depend on browser storage being writable.

A saved manual choice wins. In automatic desktop mode, supported system message
locales win: `LANGUAGE`, `LC_ALL`, `LC_MESSAGES`, then `LANG`. Within the gettext
`LANGUAGE` list, the first supported language wins. Explicit C/POSIX means
English; an unsupported explicit environment locale does not yield to a
lower-priority environment variable. Where the system locale is unavailable,
use the first supported browser language. Automatic browser mode uses ordered
browser language preferences. English is the fallback. Traditional Chinese
script/region tags do not imply a Simplified Chinese translation; explicit Hans
script tags are supported. Portuguese regional tags use the Brazilian catalogue.

Changing language loads its static catalogue and saves the preference before
updating the displayed text. Failed loads or saves leave the current choice
intact and expose a retryable error. Workspaces, open editors, filters, resource
selections and live connections stay mounted. Existing transient messages may
retain the language in which they were produced. Native confirmation
notifications read the latest stored preference when new plans arrive. With no
manual preference, native notifications use the supported environment locale
or English; the frontend can additionally detect browser language preferences.

## Scope and source

`web/src/i18n.ts` holds the English and Russian UI/provider catalogues and the
lazy loader. `web/src/locales/{zh,es,de,fr,pt,ja}.json` contains full UI and
provider catalogues. Additional languages load on demand; translating never
requires an external service at runtime. `web/src/languages.ts` defines supported
tags and native names. `internal/api/language.go` handles system tags and the
validated UI-only preference. `SetLanguage` is absent from the agent API.

`web/src/presentation.ts` translates known navigation sections, Docker resource
labels and standard table headings for display only. Resource names, API group
identifiers, Kubernetes type names, custom metadata, paths and machine values
remain original. Persistence and requests always use stable untranslated IDs.
Unknown provider messages and external server errors retain their original
text, usually English. Logs, terminal output, manifests and API documentation
are not translated. Website localization has its own independent implementation.

## Quality and verification

Preserve complete meaning, especially destructive-action warnings, unknown
outcomes, retry requirements, secret handling and configuration-reset limits.
Keep placeholders, technical identifiers, numeric limits and literal paths.
Helm Charts are packages, not diagrams; releases are installed release instances.
A command palette is a navigation interface, not an artist's palette.

Catalogue review and automated checks are not independent native-speaker
certification. Persian remains outside the current set pending terminology
review and RTL verification; this is not a claim that it cannot be translated.

`make check` includes key/placeholder parity against UI and Go provider sources,
preference/detection/error tests, real-browser language switching, denied-storage
selection and process-restart persistence. Language screenshots cover 1280 px
and 860 px windows; capture artifacts belong in `OCULAR_SCRATCH_DIR`.

The About dialog is localized in all eight languages. Product and author links
use explicit localized website URLs, including `?lang=ru` on Russian roots, so
website detection or blocked storage cannot override the selected language.
Author names, version identifiers and the formal license name remain data.
