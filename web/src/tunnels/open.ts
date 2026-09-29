import { Browser } from '@wailsio/runtime'

/** Opens a tunnel's URL in the user's browser (desktop: the system browser via Wails). */
export async function openURL(url: string, mode: 'desktop' | 'browser'): Promise<void> {
  if (mode === 'desktop') {
    await Browser.OpenURL(url)
    return
  }
  window.open(url, '_blank', 'noopener,noreferrer')
}
