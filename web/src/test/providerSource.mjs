// Test-only source reader: Node reads fixed backend catalogues directly.
// Do not expose Go source files through the Vite asset server.
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
const here = dirname(fileURLToPath(import.meta.url))

export const providerEnglish = {}
for (const [prefix, folder] of [['api', 'api'], ['compose', 'providers/compose'], ['kubernetes', 'providers/kubernetes']]) {
  const source = readFileSync(resolve(here, `../../../internal/${folder}/messages.go`), 'utf8').split('var messageTexts = map[string]string{')[1].split('\n}')[0]
  for (const entry of source.matchAll(/"([^"\n]+)":\s*("(?:[^"\\]|\\.)*")/g)) providerEnglish[`${prefix}.${entry[1]}`] = JSON.parse(entry[2])
}
