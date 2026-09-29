import { Clipboard } from '@wailsio/runtime'

// Desktop: the Wails clipboard (WebKitGTK's async clipboard API is not
// reliably permitted for the app's origin); browser: the Clipboard API
// (127.0.0.1 is a secure context).

export async function copyText(text: string, mode: 'desktop' | 'browser'): Promise<void> {
  if (mode === 'desktop') {
    try {
      await Clipboard.SetText(text)
      return
    } catch {
      // fall through
    }
  }
  await navigator.clipboard?.writeText(text).catch(() => {})
}

export async function readText(mode: 'desktop' | 'browser'): Promise<string> {
  if (mode === 'desktop') {
    try {
      return await Clipboard.Text()
    } catch {
      // fall through
    }
  }
  try {
    return (await navigator.clipboard?.readText()) ?? ''
  } catch {
    return ''
  }
}
