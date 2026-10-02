import { MIN_COLUMN_WIDTH, MAX_COLUMN_WIDTH } from './ColumnResize'

export const columnWidthsKey = (kind: string) => `columnWidths.${kind}`

export function parseColumnWidths(state: Record<string, string>): Record<string, Record<string, number>> {
  const result: Record<string, Record<string, number>> = {}
  for (const [key, json] of Object.entries(state)) {
    if (!key.startsWith('columnWidths.')) continue
    try {
      const raw: unknown = JSON.parse(json)
      if (!raw || typeof raw !== 'object' || Array.isArray(raw)) continue
      result[key.slice('columnWidths.'.length)] = Object.fromEntries(Object.entries(raw).filter(([, width]) =>
        typeof width === 'number' && Number.isFinite(width) && width >= MIN_COLUMN_WIDTH && width <= MAX_COLUMN_WIDTH))
    } catch { /* A malformed stored width does not break the table. */ }
  }
  return result
}
