// Compact value formatting for tables.

/** 45s, 12m, 3h, 5d, 2y — like kubectl's AGE. */
export function formatAge(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 48) return `${h}h`
  const d = Math.floor(h / 24)
  if (d < 730) return `${d}d`
  return `${Math.floor(d / 365)}y`
}

/** CPU cores: 0.25 → "250m", 1.5 → "1.50". */
export function formatCPU(cores: number): string {
  if (cores < 1) return `${Math.round(cores * 1000)}m`
  return cores.toFixed(cores < 10 ? 2 : 1)
}

/** Bytes in binary units: 1536 → "1.5Ki", 268435456 → "256Mi". */
export function formatBytes(n: number): string {
  const units = ['', 'Ki', 'Mi', 'Gi', 'Ti']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)}${units[i]}`
}
