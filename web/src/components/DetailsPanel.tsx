import { useRef, type ReactNode, type RefObject } from 'react'
import { t } from '../i18n'
import { PanelResize, usePanelWidths } from './PanelResize'

/** Shared resource/Helm detail surface, sizing and keyboard resize controls. */
export function DetailsPanel({ children, label, widthKey = 'details', panelRef: suppliedRef }: {
  children: ReactNode
  label: string
  widthKey?: 'details' | 'helmDetails'
  panelRef?: RefObject<HTMLElement | null>
}) {
  const localRef = useRef<HTMLElement | null>(null)
  const panelRef = suppliedRef ?? localRef
  const width = usePanelWidths(s => s[widthKey])
  return <aside ref={panelRef} style={{ width: width ?? 'min(720px,55%)', maxWidth: 'calc(100% - 100px)' }} role="dialog" aria-label={label} data-area="details" tabIndex={-1} className="resource-drawer absolute inset-y-0 right-0 z-10 flex min-w-0 flex-col border-l border-line outline-none">
    <PanelResize label={t('panels.details')} reverse value={width ?? 720} min={280} max={() => Math.max(100, (panelRef.current?.parentElement?.clientWidth ?? window.innerWidth) - 100)} onDone={size => usePanelWidths.setState({ [widthKey]: size })} />
    {children}
  </aside>
}
