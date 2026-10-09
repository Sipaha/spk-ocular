import { lazy, Suspense, useEffect, useRef, useState, useCallback } from 'react'
import { createPortal } from 'react-dom'
import { LanguageDescription } from '@codemirror/language'
import type { Client } from '../api/client'
import type { ExecInstance, FilesResponse, Ref } from '../api/types'
import { runningChannel } from '../components/useInstancePicker'
import { PanelResize } from '../components/PanelResize'
import { Select } from '../components/Select'
import { holdEdits, mayLeave } from '../edit/guard'
import { focusMark, restoreFocus } from '../shortcuts'
import { isShortcut } from '../keyboard'
import { t } from '../i18n'
import { downloadContainerFiles } from './download'
import { Loading } from './Loading'
import { FileTree } from './FileTree'
import { ancestorsOf, cleanDirectoryPath, type Directories } from './treeModel'

const Editor = lazy(() => import('../components/YamlView'))
const button = 'h-[26px] rounded border border-line px-2 text-[13px] hover:bg-hover disabled:opacity-50'
interface Container { instance: string; channel: string; title: string }
const errorText = (error: unknown) => error instanceof Error ? error.message : String(error)

export default function FileBrowser({ client, subject, selectedInstance, onClose }: { client: Client; subject: Ref; selectedInstance?: ExecInstance; onClose: () => void }) {
  // The inspector belongs to the object/container it opened on, not a later
  // background selection. Reopening Files resolves a fresh provider default.
  const [source] = useState(subject)
  const [container, setContainer] = useState<Container | null>(null)
  const [treeWidth, setTreeWidth] = useState(306)
  const [directories, setDirectories] = useState<Directories>({})
  const directoryCache = useRef<Directories>({})
  const inFlight = useRef(new Map<string, Promise<FilesResponse>>())
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set(['/']))
  const [selected, setSelected] = useState('/')
  const [directory, setDirectory] = useState('/')
  const [address, setAddress] = useState('/')
  const [doc, setDoc] = useState<FilesResponse | null>(null)
  const [text, setText] = useState('')
  const [error, setError] = useState('')
  const [initializing, setInitializing] = useState(true)
  const [busy, setBusy] = useState(false)
  const [readingPath, setReadingPath] = useState('')
  const [resolvingLink, setResolvingLink] = useState(false)
  const [writing, setWriting] = useState(false)
  const [downloading, setDownloading] = useState(false)
  const [downloaded, setDownloaded] = useState('')
  const [saved, setSaved] = useState(false)
  const [language, setLanguage] = useState('')
  const [languages, setLanguages] = useState<LanguageDescription[]>([])
  const state = useRef({ doc, text, writing })
  useEffect(() => { state.current = { doc, text, writing } }, [doc, text, writing])
  const revision = useRef('')
  const box = useRef<HTMLElement>(null)
  const [mark] = useState(focusMark)
  useEffect(() => { box.current?.focus(); return () => restoreFocus(mark) }, [mark])
  const alive = useRef(true)
  const sequence = useRef(0)
  useEffect(() => { alive.current = true; return () => { alive.current = false } }, [])
  useEffect(() => holdEdits({
    dirty: () => state.current.writing || (!!state.current.doc && state.current.text !== state.current.doc.text),
    discard: () => { setDoc(null); setText('') },
    focus: () => box.current?.querySelector<HTMLElement>('.cm-content')?.focus(),
  }), [])

  useEffect(() => {
    let live = true
    void import('@codemirror/language-data').then(module => { if (live) setLanguages(module.languages) })
    const infoRequest = selectedInstance ? Promise.resolve({instances:[selectedInstance],defaultInstance:selectedInstance.id}) : client.execInfo(source)
    infoRequest.then(info => {
      if (!live) return
      const instance = info.instances.find(item => item.id === info.defaultInstance) ?? info.instances[0]
      if (!instance) { setError(t('term.noInstances')); setInitializing(false); return }
      const channel = runningChannel(instance)
      const objectTitle = source.title || source.name
      const title = [source.scope, objectTitle !== instance.title ? objectTitle : '', instance.title, channel?.title].filter(Boolean).join(' / ')
      setContainer({ instance: instance.id, channel: channel?.id ?? '', title })
    }, failure => { if (live) { setError(errorText(failure)); setInitializing(false) } })
    return () => { live = false }
  }, [client, source, selectedInstance])

  const updateDirectory = useCallback((path: string, update: Directories[string]) => {
    directoryCache.current = { ...directoryCache.current, [path]: update }
    setDirectories(directoryCache.current)
  }, [])
  const loadDirectory = useCallback((path: string, refresh = false): Promise<FilesResponse> => {
    if (!container) return Promise.reject(new Error(t('term.noInstances')))
    const pending = inFlight.current.get(path)
    if (pending) {
      if (refresh) updateDirectory(path, {...directoryCache.current[path], refreshing:true})
      return pending
    }
    const cached = directoryCache.current[path]
    if (!refresh && cached?.entries) return Promise.resolve({ path, entries: cached.entries, text: '', configRev: revision.current })
    updateDirectory(path, { ...cached, loading: true, refreshing:refresh, error: undefined })
    const request = client.files({ ref: source, instance: container.instance, channel: container.channel, command: 'list', path, configRev: revision.current })
      .then(result => {
        if (alive.current) {
          revision.current = result.configRev
          updateDirectory(path, { entries: result.entries ?? [] })
        }
        return result
      }, failure => {
        if (alive.current) {
          const message = errorText(failure)
          updateDirectory(path, { ...cached, loading: false, refreshing:false, error: message })
          setError(`${path}: ${message}`)
        }
        throw failure
      }).finally(() => { inFlight.current.delete(path) })
    inFlight.current.set(path, request)
    return request
  }, [client, container, source, updateDirectory])

  useEffect(() => {
    if (!container) return
    const timer = setTimeout(() => {
      void loadDirectory('/').catch(() => {}).finally(() => { if (alive.current) setInitializing(false) })
    }, 0)
    return () => clearTimeout(timer)
  }, [container, loadDirectory])

  const toggleDirectory = (path: string) => {
    if (writing) return
    setSelected(path); setDirectory(path); setAddress(path)
    if (expanded.has(path)) {
      setExpanded(previous => new Set([...previous].filter(item => item !== path && !item.startsWith(path === '/' ? '/' : path + '/'))))
      // Loaded branches remain cached until an explicit Refresh. Collapsing
      // changes only visibility, including while the first request is pending.
    } else {
      setExpanded(previous => new Set(previous).add(path))
      setError('')
      void loadDirectory(path).catch(() => {})
    }
  }
  const refreshDirectory = (path: string) => {
    if (writing) return
    setExpanded(previous => new Set(previous).add(path))
    setError('')
    void loadDirectory(path, true).catch(() => {})
  }
  const revealDirectory = async (value: string) => {
    if (writing || initializing) return
    const path = cleanDirectoryPath(value)
    if (!path) { setError(t('files.absolutePath')); return }
    setError('')
    try {
      for (const ancestor of ancestorsOf(path)) {
        await loadDirectory(ancestor)
        if (!alive.current) return
        setExpanded(previous => new Set(previous).add(ancestor))
      }
      setDirectory(path); setAddress(path); setSelected(path)
    } catch { /* The failed branch keeps its own visible error. */ }
  }
  const requestFile = async (command: 'read' | 'write', path: string) => {
    if (!container || busy) return
    const current = ++sequence.current
    setBusy(true); setReadingPath(command === 'read' ? path : ''); setWriting(command === 'write'); setError(''); setSaved(false)
    try {
      const result = await client.files({ ref: source, instance: container.instance, channel: container.channel, command, path, configRev: revision.current,
        ...(command === 'write' ? { text: state.current.text, expect: state.current.doc?.version } : {}) })
      if (!alive.current || current !== sequence.current) return
      revision.current = result.configRev
      setDoc(result); setText(result.text); setSelected(result.path)
      if (command === 'read') setLanguage('')
      setSaved(command === 'write')
    } catch (failure) { if (alive.current && current === sequence.current) setError(errorText(failure)) }
    finally { if (alive.current && current === sequence.current) { setBusy(false); setReadingPath(''); setWriting(false) } }
  }
  const followLink = (path: string, folder: boolean) => {
    if (!container || busy || resolvingLink || writing) return
    const follow = async () => {
      setResolvingLink(true); setError('')
      if (!folder) setReadingPath(path)
      try {
        const result = await client.files({ ref: source, instance: container.instance, channel: container.channel, command:'resolve', path, configRev:revision.current })
        if (!alive.current) return
        const parent = folder ? result.path : result.path.slice(0, result.path.lastIndexOf('/')) || '/'
        for (const ancestor of ancestorsOf(parent)) {
          await loadDirectory(ancestor)
          if (!alive.current) return
          setExpanded(previous => new Set(previous).add(ancestor))
        }
        setSelected(result.path); setDirectory(parent); setAddress(parent)
        if (!folder) await requestFile('read', result.path)
      } catch (failure) { if (alive.current) setError(errorText(failure)) }
      finally { if (alive.current) { setResolvingLink(false); setReadingPath('') } }
    }
    if (folder) void follow()
    else mayLeave(() => void follow())
  }
  const download = async (path: string) => {
    if (!container || downloading) return
    setDownloading(true); setDownloaded(''); setError('')
    try {
      const destination = await downloadContainerFiles({ ref: source, instance: container.instance, channel: container.channel, path, configRev: revision.current, command: 'read' })
      if (alive.current && destination) setDownloaded(destination)
    } catch (failure) { if (alive.current) setError(errorText(failure)) }
    finally { if (alive.current) setDownloading(false) }
  }
  const openFile = (path: string) => { if (!busy) mayLeave(() => void requestFile('read', path)) }
  const close = () => { if (!writing) mayLeave(onClose) }
  const autoLanguage = doc ? LanguageDescription.matchFilename(languages, doc.path)?.name : undefined
  const directoryLoading = Object.values(directories).some(item => item.loading)

  return createPortal(
    <div data-file-overlay className="fixed inset-0 z-30 flex items-center justify-center bg-black/50 p-[12px]" onMouseDown={event => { if (event.target === event.currentTarget) close() }}>
      <section ref={box} tabIndex={-1} role="dialog" aria-modal="true" aria-label={t('files.title')}
        className="relative flex h-[calc(100dvh-24px)] w-[min(1800px,calc(100vw-24px))] flex-col border border-line bg-panel"
        onKeyDown={event => {
          if (event.key === 'Tab') {
            const controls = [...(box.current?.querySelectorAll<HTMLElement>('button:not(:disabled):not([tabindex="-1"]), input:not(:disabled), [tabindex="0"], [contenteditable="true"]') ?? [])]
              .filter(item => item.getClientRects().length > 0 && !item.closest('[inert]'))
            const first = controls[0], last = controls.at(-1)
            if (event.shiftKey && (document.activeElement === first || document.activeElement === box.current)) { event.preventDefault(); last?.focus() }
            else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
          }
          if (event.key === 'Escape') { event.stopPropagation(); close() }
          if (!event.altKey && isShortcut(event, 'KeyS', { ctrl: true, shift: false })) { event.preventDefault(); if (doc && !busy) void requestFile('write', doc.path) }
        }}>
        <header className="flex h-[35px] shrink-0 items-center gap-3 border-b border-line px-3 py-[4px]" data-file-header>
          <strong>{t('files.title')}</strong>
          <span className="min-w-0 flex-1 truncate text-sm text-fg-muted" data-file-context title={container?.title}>{container?.title ?? source.name}</span>
          <button className={button} disabled={writing} onClick={close} aria-label={t('drawer.close')}>×</button>
        </header>
        <div className="flex h-[35px] shrink-0 items-center gap-2 border-b border-line px-3 py-[4px]" data-file-address>
          <form className="flex flex-1 gap-2" onSubmit={event => { event.preventDefault(); void revealDirectory(address) }}>
            <input disabled={initializing || writing} aria-label={t('files.path')} className="h-[26px] min-w-0 flex-1 border border-line bg-app px-2 font-mono text-[13px]"
              value={address} onChange={event => setAddress(event.target.value)} />
            <button className={button} disabled={initializing || writing}>{t('files.go')}</button>
          </form>
          <button className={button} disabled={initializing || writing || !!directories[directory]?.loading} onClick={() => refreshDirectory(directory)}>{t('files.refresh')}</button>
        </div>
        {error && <div role="alert" data-file-error className="absolute right-3 bottom-[39px] z-30 flex max-w-[min(600px,calc(100%-24px))] items-start gap-3 border border-danger/50 bg-panel px-3 py-2 text-sm text-danger shadow-lg">
          <p className="max-h-40 min-w-0 overflow-auto whitespace-pre-wrap break-words">{error}</p>
          <button type="button" className="flex h-6 w-6 shrink-0 items-center justify-center hover:bg-hover" aria-label={t('files.dismissError')}
            onClick={() => { setError(''); box.current?.focus({preventScroll:true}) }}>×</button>
        </div>}
        <div className="flex min-h-0 flex-1">
          <div className="relative min-h-0 shrink-0" style={{width:treeWidth,maxWidth:'65%'}} data-file-tree-panel>
          <FileTree directories={directories} expanded={expanded} selected={selected} disabled={writing || initializing || downloading || resolvingLink} onSelect={(path, folder) => { setSelected(path); const parent = folder ? path : path.slice(0, path.lastIndexOf('/')) || '/'; setDirectory(parent); setAddress(parent) }}
            onToggle={toggleDirectory} onOpen={openFile} onRefresh={refreshDirectory} onDownload={path => void download(path)} onFollowLink={followLink} />
          <PanelResize label={t('files.resizeTree')} outside value={treeWidth} min={140} max={() => (box.current?.clientWidth ?? 1200)*0.65} onDone={setTreeWidth} />
          </div>
          <div className="relative flex min-w-0 flex-1 flex-col" data-file-editor-panel aria-busy={!!readingPath || undefined}>
            {doc ? <>
              <div className="flex h-[35px] shrink-0 items-center gap-2 border-b border-line px-3 py-[4px]" data-file-document-header>
                <span className="min-w-0 flex-1 truncate font-mono text-sm" title={doc.path}>{doc.path}{text !== doc.text ? ' *' : ''}</span>
                <button className={button} disabled={busy || text === doc.text} onClick={() => void requestFile('write', doc.path)}>{t('files.save')}</button>
              </div>
              <div className="min-h-0 flex-1" inert={busy}>
                <Suspense fallback={<div className="flex h-full items-center justify-center"><Loading /></div>}><Editor key={doc.path} filename={doc.path} language={language} text={doc.text} editable onChange={setText} label={doc.path} /></Suspense>
              </div>
            </> : !readingPath && <p className="m-auto text-sm text-fg-muted">{t('files.choose')}</p>}
            {readingPath && <div className="absolute inset-0 z-10 flex items-center justify-center bg-panel/90" data-file-read-loading><Loading label={t('files.loadingFile', {path:readingPath})} /></div>}
          </div>
        </div>
        <footer className="flex h-[27px] shrink-0 items-center gap-3 border-t border-line px-3 py-[2px] text-[12px] leading-[18px] text-fg-muted" data-file-footer>
          <span className="min-w-0 flex-1 truncate" aria-live="polite">{busy || initializing || directoryLoading || downloading || resolvingLink ? t('app.loading') : downloaded ? t('files.downloaded', { path: downloaded }) : saved ? t('files.saved') : t('files.limit')}</span>
          {doc && <Select label={t('files.language')} className="file-footer-select text-[12px]" value={language} options={[
            { value: '', label: `${autoLanguage ?? t('files.plain')} · ${t('files.auto')}` },
            { value: 'plain', label: t('files.plain') }, ...languages.map(item => ({ value: item.name, label: item.name })),
          ]} onChange={setLanguage} />}
        </footer>
      </section>
    </div>, document.body,
  )
}
