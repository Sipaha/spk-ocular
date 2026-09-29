// A command line → argv, without a shell: nothing is expanded or run.
// Whitespace separates arguments; '…' is literal; "…" is literal except
// \" and \\; a backslash outside quotes takes the next character as is;
// '' or "" is an empty argument. An unclosed quote is an error.

export class ArgvError extends Error {}

export function parseArgv(line: string): string[] {
  const out: string[] = []
  let cur = ''
  let has = false // an argument is being built (possibly empty: '')
  let i = 0
  while (i < line.length) {
    const ch = line[i]
    if (ch === ' ' || ch === '\t' || ch === '\n') {
      if (has) out.push(cur)
      cur = ''
      has = false
      i++
    } else if (ch === "'") {
      const end = line.indexOf("'", i + 1)
      if (end < 0) throw new ArgvError('unclosed single quote')
      cur += line.slice(i + 1, end)
      has = true
      i = end + 1
    } else if (ch === '"') {
      i++
      has = true
      let closed = false
      while (i < line.length) {
        const c = line[i]
        if (c === '"') {
          closed = true
          i++
          break
        }
        if (c === '\\' && (line[i + 1] === '"' || line[i + 1] === '\\')) {
          cur += line[i + 1]
          i += 2
          continue
        }
        cur += c
        i++
      }
      if (!closed) throw new ArgvError('unclosed double quote')
    } else if (ch === '\\') {
      if (i + 1 >= line.length) throw new ArgvError('a backslash at the end escapes nothing')
      cur += line[i + 1]
      has = true
      i += 2
    } else {
      cur += ch
      has = true
      i++
    }
  }
  if (has) out.push(cur)
  return out
}

/** argv → a line parseArgv reads back to the same argv (for the reconnect dialog). */
export function formatArgv(argv: string[]): string {
  return argv.map((a) => (a !== '' && /^[\w@%+=:,./-]+$/.test(a) ? a : `'${a.replace(/'/g, `'\\''`)}'`)).join(' ')
}
