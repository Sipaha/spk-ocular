// Search and filter matching. Plain search and the "*" filter are linear
// (indexOf) and run on the main thread; regular expressions can backtrack
// catastrophically, so they only ever run in a worker (regexSearch.ts).

export type Range = [start: number, end: number]

/** Ranges per line are capped: a highlight, not an index. */
export const MAX_RANGES = 200

/** Case-insensitive occurrences of q (already lower-cased) in text. */
export function plainRanges(text: string, qLower: string): Range[] {
  if (!qLower) return []
  const hay = text.toLowerCase()
  // toLowerCase can change the length (e.g. "İ"): fall back to "no ranges"
  // rather than highlight the wrong characters.
  if (hay.length !== text.length) return hay.includes(qLower) ? [[0, 0]] : []
  const out: Range[] = []
  for (let i = hay.indexOf(qLower); i >= 0 && out.length < MAX_RANGES; i = hay.indexOf(qLower, i + qLower.length)) {
    out.push([i, i + qLower.length])
  }
  return out
}

/**
 * The hide-filter: case-insensitive, "*" = any text. Matched part by part
 * with indexOf (leftmost placement is always right for "*" patterns), so it
 * is linear — no regular expression involved. Like the launcher's filter it
 * matches anywhere in the line. Shorter than 2 characters = no filter.
 */
export function compileFilter(filter: string): ((text: string) => boolean) | null {
  const f = filter.trim().toLowerCase()
  if (f.replaceAll('*', '').length < 2) return null
  const parts = f.split('*')
  return (text) => {
    const t = text.toLowerCase()
    let at = 0
    for (let i = 0; i < parts.length; i++) {
      const p = parts[i]
      if (!p) continue
      const j = t.indexOf(p, at)
      if (j < 0) return false
      at = j + p.length
    }
    return true
  }
}

/** A regex as the user typed it: compiled (case-insensitive) or an error. */
export function compileRegex(q: string): { re: RegExp } | { error: string } {
  try {
    return { re: new RegExp(q, 'gi') }
  } catch (e) {
    return { error: e instanceof Error ? e.message : String(e) }
  }
}

/** Match ranges of re in text (the worker's side). Empty matches are skipped. */
export function regexRanges(re: RegExp, text: string): Range[] {
  const out: Range[] = []
  re.lastIndex = 0
  for (let m = re.exec(text); m && out.length < MAX_RANGES; m = re.exec(text)) {
    if (m[0].length === 0) {
      re.lastIndex++
      continue
    }
    out.push([m.index, m.index + m[0].length])
  }
  return out
}
