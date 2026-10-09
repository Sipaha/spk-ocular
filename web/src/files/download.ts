import { isDesktop } from '../api/client'
import type { FilesRequest } from '../api/types'
import { t } from '../i18n'

export async function downloadContainerFiles(req: FilesRequest): Promise<string | null> {
  if (!isDesktop()) throw new Error(t('files.nativeDownload'))
  const { Call } = await import('@wailsio/runtime')
  const destination = await Call.ByName('github.com/spk/spk-ocular/internal/desktop.FileDownloads.Download', req, t('files.downloadDialog'), t('files.download')) as string
  return destination || null
}
