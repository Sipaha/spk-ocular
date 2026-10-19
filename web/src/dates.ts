/** Display calendar dates in local machine time, with unambiguous day-first order. */
const pad = (value: number) => String(value).padStart(2, '0')
export function localDate(value: string | number): string {
 const date = new Date(value)
 if (!Number.isFinite(date.getTime())) return typeof value === 'string' ? value : '—'
 return `${pad(date.getDate())}.${pad(date.getMonth()+1)}.${String(date.getFullYear()).padStart(4,'0')}`
}
export function localDateTime(value: string | number): string {
 const date = new Date(value)
 if (!Number.isFinite(date.getTime())) return typeof value === 'string' ? value : '—'
 return `${localDate(value)} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}
export function isoDateTime(value: string | number): string {
 if (typeof value === 'string') return value
 const date = new Date(value)
 return Number.isFinite(date.getTime()) ? date.toISOString() : ''
}
