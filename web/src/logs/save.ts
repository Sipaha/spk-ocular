import { isDesktop, type Client } from '../api/client'

/** A file name for saved logs: "<name>_<yyyymmdd_hhmmss>.log". */
export function logFileName(name: string, d = new Date()): string {
  const p = (n: number) => String(n).padStart(2, '0')
  const ts = `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}_${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`
  return `${name.replace(/[^\w.-]+/g, '_')}_${ts}.log`
}

/**
 * Saves text: the browser downloads a Blob; the desktop webview has no
 * download manager, so the app writes the file to the downloads folder
 * (POST <streamBase>/save with a string body — never a Blob through the
 * webview) and returns its path.
 */
export async function saveLogs(client: Client, name: string, text: string): Promise<string | null> {
  if (!isDesktop()) {
    const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }))
    const a = document.createElement('a')
    a.href = url
    a.download = name
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 5000)
    return null
  }
  const base = await client.streamBase()
  const res = await fetch(`${base}/save?name=${encodeURIComponent(name)}`, { method: 'POST', body: text, headers: { 'Content-Type': 'text/plain;charset=UTF-8' } })
  if (!res.ok) throw new Error((await res.text()) || `HTTP ${res.status}`)
  return ((await res.json()) as { path: string }).path
}
