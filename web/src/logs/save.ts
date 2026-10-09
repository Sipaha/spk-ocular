import { isDesktop } from '../api/client'
import { stripAnsi } from './ansi'
import { t } from '../i18n'

/** A suggested UTF-8 log filename: "<name>_<yyyymmdd_hhmmss>.log". */
export function logFileName(name: string, d = new Date()): string {
  const p = (n: number) => String(n).padStart(2, '0')
  const ts = `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}_${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`
  return `${name.replace(/[^\w.-]+/g, '_').slice(0, 140) || 'logs'}_${ts}.log`
}

interface WritableLogFile { write: (text: string) => Promise<void>; close: () => Promise<void>; abort: () => Promise<void> }
interface SavePicker { showSaveFilePicker?: (options: { suggestedName: string; types: { description: string; accept: Record<string, string[]> }[] }) => Promise<{ name: string; createWritable: () => Promise<WritableLogFile> }> }

/** Remove terminal formatting without touching ordinary brackets or Unicode. */
export function plainLogDownload(text: string): string {
  if (!text.includes('\x1b') && !text.includes('\u009b') && !text.includes('\u009c') && !text.includes('\u009d')) return text
  const normalized = text.replaceAll('\u009b', '\x1b[').replaceAll('\u009d', '\x1b]').replaceAll('\u009c', '\x1b\\')
  return stripAnsi(normalized).plain
}

/** Native windows choose the destination through Wails. Browsers use their
 * save picker where available, with ordinary download-manager fallback. */
export async function downloadLogs(name: string, text: string, windowID = ''): Promise<string | null> {
  text = plainLogDownload(text)
  if (isDesktop()) {
    const { Call } = await import('@wailsio/runtime')
    const path = await Call.ByName('github.com/spk/spk-ocular/internal/desktop.LogWindows.ExportLogs', windowID, name, t('logs.downloadDialog'), t('logs.save'), text) as string
    return path || null
  }
  const picker = window as Window & SavePicker
  if (picker.showSaveFilePicker) {
    try {
      const handle = await picker.showSaveFilePicker({ suggestedName: name, types: [{ description: t('logs.downloadDialog'), accept: { 'text/plain': ['.log', '.txt'] } }] })
      const writable = await handle.createWritable()
      try { await writable.write(text); await writable.close() }
      catch (error) { await writable.abort().catch(() => {}); throw error }
      return handle.name
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return null
      throw error
    }
  }
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 5000)
  return null
}
