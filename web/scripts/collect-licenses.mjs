// Collect exact installed production dependencies, including lazy imports.
// Tailwind's generated CSS is shipped too, although its compiler is a dev tool.
import { readFileSync, readdirSync, realpathSync, existsSync, writeFileSync } from 'node:fs'
import { dirname, join, parse } from 'node:path'

const legalName = /^(?:(?:third[-_ ]party[-_ ])?(?:licen[cs]es?|notices?)|copying|copyright|patents|authors)(?:[._ -].*)?$/i
const seen = new Map()
function resolveDependency(name, from) {
  for (let dir = from; ; dir = dirname(dir)) {
    const candidate = join(dir, 'node_modules', name)
    if (existsSync(join(candidate, 'package.json'))) return realpathSync(candidate)
    if (dir === parse(dir).root) throw new Error(`Cannot resolve license input ${name} from ${from}`)
  }
}
function collect(dir, followDependencies = true) {
  dir = realpathSync(dir)
  if (seen.has(dir)) return
  const pkg = JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8'))
  const files = readdirSync(dir, { withFileTypes: true })
    .filter(f => f.isFile() && legalName.test(f.name))
    .sort((a, b) => a.name.localeCompare(b.name, 'en'))
    .map(f => ({ name: f.name, text: readFileSync(join(dir, f.name), 'utf8') }))
  // The published Wails runtime omits LICENSE. The Go collector supplies the
  // matching Wails module's MIT text and verifies the two versions agree.
  const hasLicense = files.some(f => /licen[cs]e|copying/i.test(f.name) && f.text.trim())
  if (!hasLicense && pkg.name !== '@wailsio/runtime') throw new Error(`Missing license text: ${pkg.name}@${pkg.version}`)
  if (pkg.license !== 'MIT') throw new Error(`Review new frontend license: ${pkg.name}@${pkg.version}: ${pkg.license}`)
  seen.set(dir, { name: pkg.name, version: pkg.version, license: pkg.license, files })
  if (followDependencies) {
    for (const name of Object.keys(pkg.dependencies ?? {}).sort()) collect(resolveDependency(name, dir))
  }
}
const root = realpathSync('.')
const pkg = JSON.parse(readFileSync('package.json', 'utf8'))
for (const name of [...Object.keys(pkg.dependencies), 'tailwindcss'].sort()) collect(resolveDependency(name, root))
// Build tools inject module-preload and bundler runtime helpers into the JS.
// Their build-time dependency trees are not shipped. Preserve the tools' own
// texts and bundled third-party notices without walking those trees.
const vite = resolveDependency('vite', root)
collect(vite, false)
collect(resolveDependency('rolldown', vite), false)
const packages = [...seen.values()].sort((a, b) => a.name.localeCompare(b.name, 'en'))
writeFileSync('license-inputs.json', JSON.stringify({ schema: 1, packages }, null, 2) + '\n')
console.log(`licenses: collected ${packages.length} frontend components`)
