// Post-build guard (docs/architecture.md): the entry chunk stays small and
// heavy libraries only ever load lazily.
import { readFileSync, readdirSync } from 'node:fs'
import { gzipSync } from 'node:zlib'
import { join } from 'node:path'

const ENTRY_GZ_BUDGET = 300 * 1024
// Markers of libraries that must never be in the entry chunk.
const LAZY_ONLY = [
  ['xterm', /xterm-helper-textarea|class Terminal\b/],
  ['codemirror', /cm-editor|@codemirror/],
]

const dir = 'dist/assets'
const html = readFileSync('dist/index.html', 'utf8')
const entries = readdirSync(dir).filter((f) => f.endsWith('.js') && html.includes(f))
if (entries.length === 0) {
  console.error('check-bundle: no entry chunk referenced from dist/index.html')
  process.exit(1)
}
let failed = false
for (const f of entries) {
  const src = readFileSync(join(dir, f))
  const gz = gzipSync(src).length
  const kb = (n) => `${(n / 1024).toFixed(1)} KB`
  console.log(`check-bundle: ${f} ${kb(src.length)} (${kb(gz)} gz), budget ${kb(ENTRY_GZ_BUDGET)} gz`)
  if (gz > ENTRY_GZ_BUDGET) {
    console.error(`check-bundle: entry chunk ${f} is over budget`)
    failed = true
  }
  const text = src.toString('utf8')
  for (const [name, re] of LAZY_ONLY) {
    if (re.test(text)) {
      console.error(`check-bundle: ${name} landed in the entry chunk ${f}; import it lazily`)
      failed = true
    }
  }
}
process.exit(failed ? 1 : 0)
