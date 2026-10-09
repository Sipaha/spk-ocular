import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { Loading } from './Loading'
import { Menu } from '../actions/Menu'
import { t } from '../i18n'
import { FileIcon, FolderIcon, LinkArrowIcon } from '../components/icons'
import { fileTreeRows, type Directories, type FileTreeRow } from './fileTree'

const rowHeight = 24
interface Props {
  directories: Directories
  expanded: ReadonlySet<string>
  selected: string
  disabled: boolean
  onSelect: (path: string, directory: boolean) => void
  onToggle: (path: string) => void
  onOpen: (path: string) => void
  onRefresh: (path: string) => void
  onDownload: (path: string) => void
  onFollowLink: (path: string, directory: boolean) => void
}

export function FileTree({ directories, expanded, selected, disabled, onSelect, onToggle, onOpen, onRefresh, onDownload, onFollowLink }: Props) {
  const host = useRef<HTMLElement>(null)
  const id = useId()
  const [menu, setMenu] = useState<{ path: string; directory: boolean; symlink: boolean; x: number; y: number } | null>(null)
  const rows = useMemo(() => fileTreeRows(directories, expanded), [directories, expanded])
  const rowKey = useCallback((index: number) => rows[index].path, [rows])
  // eslint-disable-next-line react-hooks/incompatible-library -- consumed only by this tree
  const virtualizer = useVirtualizer({ count: rows.length, getScrollElement: () => host.current, estimateSize: () => rowHeight, overscan: 8, getItemKey: rowKey })
  const virtualizerRef = useRef(virtualizer)
  useEffect(() => { virtualizerRef.current = virtualizer })
  const selectedIndex = rows.findIndex(row => row.path === selected)
  useEffect(() => {
    if (selectedIndex >= 0) virtualizerRef.current.scrollToIndex(selectedIndex, { align: 'auto' })
  }, [selectedIndex, selected])
  const rowID = (path: string) => `${id}-${encodeURIComponent(path)}`
  const select = (row: FileTreeRow) => {
    onSelect(row.path, row.directory)
    host.current?.focus({ preventScroll: true })
  }
  const keyDown = (event: KeyboardEvent) => {
    if (disabled || !rows.length || (event.target as HTMLElement).closest('[role="menu"]')) return
    const navigable = rows.filter(row => !row.placeholder)
    if (!navigable.length) return
    const index = Math.max(0, navigable.findIndex(row => row.path === selected))
    const row = navigable[index]
    const move = (next: number) => select(navigable[Math.max(0, Math.min(navigable.length - 1, next))])
    switch (event.key) {
      case 'ArrowDown': move(index + 1); break
      case 'ArrowUp': move(index - 1); break
      case 'Home': move(0); break
      case 'End': move(navigable.length - 1); break
      case 'ArrowRight':
        if (row.directory && !row.expanded) onToggle(row.path)
        else if (navigable[index + 1]?.parent === row.path) move(index + 1)
        break
      case 'ArrowLeft':
        if (row.directory && row.expanded) onToggle(row.path)
        else if (row.parent) onSelect(row.parent, true)
        break
      case 'ContextMenu':
        { const bounds = document.getElementById(rowID(row.path))?.getBoundingClientRect(); if (bounds) setMenu({path:row.path,directory:row.directory,symlink:row.symlink,x:bounds.x+30,y:bounds.bottom}) }
        break
      case 'F10':
        if (!event.shiftKey) return
        { const bounds = document.getElementById(rowID(row.path))?.getBoundingClientRect(); if (bounds) setMenu({path:row.path,directory:row.directory,symlink:row.symlink,x:bounds.x+30,y:bounds.bottom}) }
        break
      case 'Enter': if (row.directory) onToggle(row.path); else onOpen(row.path); break
      default: return
    }
    event.preventDefault()
    event.stopPropagation()
  }
  return (
    <nav ref={host} role="tree" tabIndex={0} aria-label={t('files.title')} aria-activedescendant={selectedIndex >= 0 ? rowID(selected) : undefined}
      className="file-tree-scroll h-full w-full select-none overflow-auto border-r border-line bg-sidebar font-mono antialiased outline-none" style={{WebkitUserSelect:'none'}} onKeyDown={keyDown}>
      <div style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
        {virtualizer.getVirtualItems().map(item => {
          const row = rows[item.index]
          if (row.placeholder) return <div key={item.key} role="treeitem" aria-disabled="true" aria-label={t(row.empty ? 'files.noElements' : 'app.loading')} aria-level={row.depth+1} data-file-loading-child={!row.empty || undefined} data-file-empty-child={row.empty || undefined}
            style={{position:'absolute',top:item.start,left:0,width:'100%',height:rowHeight,paddingLeft:`calc(${6+row.depth*14}px + 1.25rem)`}} className="flex items-center text-[13px] leading-5 text-fg-subtle">{row.empty ? <span>{t('files.noElements')}</span> : <Loading />}</div>
          return (
            <div key={item.key} id={rowID(row.path)} role="treeitem" aria-label={row.name} aria-level={row.depth + 1}
              aria-selected={selected === row.path} aria-expanded={row.directory ? row.expanded : undefined} aria-busy={row.loading || undefined}
              data-file-path={row.path} data-file-depth={row.depth}
              style={{ position: 'absolute', top: item.start, left: 0, width: '100%', height: rowHeight, paddingLeft: 6 + row.depth * 14 }}
              className={`flex cursor-default items-center gap-1 pr-6 text-[13px] leading-5 hover:bg-hover ${selected === row.path ? 'bg-hover text-fg' : 'text-fg-muted'}`}
              title={[row.path, row.symlink && row.target ? t('files.linkTarget', {path:row.target}) : '', row.error].filter(Boolean).join('\n')}
              onContextMenu={event => { event.preventDefault(); if(!disabled) { select(row); setMenu({path:row.path,directory:row.directory,symlink:row.symlink,x:event.clientX,y:event.clientY}) } }}
              onClick={() => { if (!disabled) select(row) }}
              onDoubleClick={() => { if (!disabled) { if (row.directory) onToggle(row.path); else onOpen(row.path) } }}>
              {row.directory ? (
                <button type="button" tabIndex={-1} disabled={disabled} aria-label={t(row.expanded ? 'files.collapse' : 'files.expand', { path: row.path })}
                  className="flex h-5 w-4 shrink-0 items-center justify-center text-fg-subtle hover:text-fg"
                  onClick={event => { event.stopPropagation(); select(row); onToggle(row.path) }} onDoubleClick={event => event.stopPropagation()}>
                  <svg aria-hidden="true" viewBox="0 0 16 16" className={`h-3 w-3 ${row.expanded ? 'rotate-90' : ''}`} fill="none" stroke="currentColor" strokeWidth="1.5"><path d="m6 4 4 4-4 4" /></svg>
                </button>
              ) : <span className="w-4 shrink-0" />}
              {row.directory ? <FolderIcon open={row.expanded} className="h-4 w-4 shrink-0 text-fg-muted" /> : <FileIcon className="h-4 w-4 shrink-0 text-fg-subtle" />}
              <span className="min-w-0 truncate">{row.name}</span>
              {row.symlink && <span data-file-link-indicator className="shrink-0"><LinkArrowIcon className="h-[14px] w-[14px] text-accent" /></span>}
              {(row.count !== undefined || row.error || (row.loading && directories[row.path]?.refreshing)) && <span className="ml-auto inline-flex shrink-0 items-center gap-2">
                {row.count !== undefined && <span data-file-entry-count className="text-[12px] text-fg-subtle" title={t('files.elementCount', {count:row.count})}>{row.count}</span>}
                {row.loading && directories[row.path]?.refreshing && <span data-file-refresh-loading><Loading label={t('files.refreshing')} compact /></span>}
                {row.error && <span aria-hidden="true" className="text-danger">!</span>}
              </span>}
            </div>
          )
        })}
      </div>
      {menu && <Menu at={menu} label={menu.path} onClose={() => setMenu(null)} items={[...(menu.directory ? [{id:'refresh',label:t('files.refresh'),disabled,onSelect:()=>onRefresh(menu.path)}] : []), ...(menu.symlink ? [{id:'follow',label:t('files.followLink'),disabled,onSelect:()=>onFollowLink(menu.path,menu.directory)}] : []), {id:'download',label:t('files.download'),disabled,onSelect:()=>onDownload(menu.path)}]} />}
    </nav>
  )
}
