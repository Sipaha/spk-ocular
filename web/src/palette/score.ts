// A fuzzy scorer for the palette: the query's letters in order (a
// subsequence), ranked exact > prefix > word start > substring > scattered,
// consecutive letters and word starts rewarded, shorter texts first among
// equals. Case-insensitive in any script (Latin and Cyrillic as typed; the
// keyboard layout is not guessed).

const WORD_BREAK = /[\s\-_./:@]/

/** null: no match; else higher is better (0 for an empty query). */
export function fuzzyScore(query: string, text: string): number | null {
  const q = query.trim().toLocaleLowerCase()
  if (!q) return 0
  const s = text.toLocaleLowerCase()
  const lengthPenalty = s.length / 1000
  if (s === q) return 10_000
  if (s.startsWith(q)) return 8_000 - lengthPenalty
  const at = s.indexOf(q)
  if (at > 0 && WORD_BREAK.test(s[at - 1])) return 6_000 - at - lengthPenalty
  if (at > 0) return 4_000 - at - lengthPenalty
  // Scattered: greedy, preferring the next word start for each letter.
  let score = 0
  let pos = 0
  let prev = -2
  for (const ch of q) {
    let found = -1
    for (let i = pos; i < s.length; i++) {
      if (s[i] !== ch) continue
      if (found < 0) found = i
      if (i === 0 || WORD_BREAK.test(s[i - 1])) {
        found = i
        break
      }
    }
    if (found < 0) return null
    const wordStart = found === 0 || WORD_BREAK.test(s[found - 1])
    score += 10 + (wordStart ? 30 : 0) + (found === prev + 1 ? 15 : 0) - Math.min(found - pos, 10)
    prev = found
    pos = found + 1
  }
  return score - lengthPenalty
}
