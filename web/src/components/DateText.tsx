import { localDateTime } from '../dates'

/** Format provider facts, without rewriting their raw API or YAML values. */
export function DateText({ value, tooltip = true }: { value: string; tooltip?: boolean }) {
 const match = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2}))($|, )/.exec(value)
 if (!match || !Number.isFinite(Date.parse(match[1]))) return value
 const timestamp = match[1]
 return <><time className="tabular-nums break-normal" dateTime={timestamp} title={tooltip ? timestamp : undefined}>{localDateTime(timestamp)}</time>{value.slice(timestamp.length)}</>
}
