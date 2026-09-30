/** The text without metadata.resourceVersion: every write moves it, so a
 * result is compared with the expected one without it. */
export function withoutVersion(text: string): string {
  const lines = text.split('\n')
  let inMeta = false
  return lines
    .filter((l) => {
      if (/^\S/.test(l)) inMeta = l === 'metadata:'
      return !(inMeta && /^ {2}resourceVersion:/.test(l))
    })
    .join('\n')
}
