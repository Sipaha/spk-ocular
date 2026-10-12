// Physical Russian/Ukrainian keyboard keys mapped to English QWERTY.
// This is layout recovery, not transliteration or a language guess.
const normal = 'ёйцукенгшщзхъфывапролджэячсмитьбю'
const english = '`qwertyuiop[]asdfghjkl;\'zxcvbnm,.'
const shifted = '~QWERTYUIOP{}ASDFGHJKL:"ZXCVBNM<>'
const keys = new Map<string, string>()
for (let i = 0; i < normal.length; i++) {
  keys.set(normal[i], english[i])
  keys.set(normal[i].toUpperCase(), shifted[i])
}
for (const [from, to] of [['ґ', '`'], ['Ґ', '~'], ['і', 's'], ['І', 'S'], ['ї', ']'], ['Ї', '}'], ['є', "'"], ['Є', '"']]) keys.set(from, to)

/** One alternate query; keep Latin text, numbers and punctuation as typed.
 * Explicit palette commands keep their original grammar. */
export function englishKeyboardQuery(input: string): string | null {
  if (input.trimStart().startsWith(':')) return null
  let changed = false
  const mapped = Array.from(input, letter => {
    const replacement = keys.get(letter)
    if (replacement === undefined) return letter
    changed = true
    return replacement
  }).join('')
  return changed && !mapped.trimStart().startsWith(':') ? mapped : null
}
