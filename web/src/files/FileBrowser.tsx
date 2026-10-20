import type {EditorSession} from '../components/YamlView'
import {holdDockClose} from '../dock/store'
import {FileDeleteDialog} from './FileDeleteDialog'
import {FileNameDialog} from './FileNameDialog'
import {ActionIconButton} from '../components/ActionIconButton'
import {ActionIcon,WindowActionIcon} from '../components/icons'
import { RefreshButton } from '../components/RefreshButton'
import { lazy, Suspense, useEffect, useLayoutEffect, useRef, useState, useCallback } from 'react'
import { createPortal } from 'react-dom'
import { LanguageDescription } from '@codemirror/language'
import type { Client } from '../api/client'
import type { ExecInstance, FilesResponse, Ref } from '../api/types'
import { runningChannel } from '../components/useInstancePicker'
import { PanelResize } from '../components/PanelResize'
import { Select } from '../components/Select'
import { holdEdits, mayLeave, mayLeaveMatching } from '../edit/guard'
import { focusMark, restoreFocus, toolWindowShortcut } from '../shortcuts'
import { isShortcut } from '../keyboard'
import { t } from '../i18n'
import { downloadContainerFiles } from './download'
import { Loading } from './Loading'
import { FileTree } from './FileTree'
import { ancestorsOf, cleanDirectoryPath, type Directories } from './treeModel'

const Editor = lazy(() => import('../components/YamlView'))
const button = 'h-[26px] rounded border border-line px-2 text-[13px] hover:bg-hover disabled:opacity-50'
export interface Container { instance: string; channel: string; title: string }
const errorText = (error: unknown) => error instanceof Error ? error.message : String(error)

export interface FilesSnapshot {
 container:Container|null;treeWidth:number;directories:Directories;expanded:string[];selected:string;directory:string;address:string
 doc:FilesResponse|null;text:string;error:string;revision:string;language:string;saved:boolean;downloaded:string
 busy:boolean;writing:boolean;initializing:boolean;documentEpoch:number;editor?:{path:string;epoch:number;session:EditorSession}
}
export interface FilesTransfer {initial?:FilesSnapshot;capture?:(snapshot:FilesSnapshot)=>void;managed?:boolean;onWindowAction?:()=>void;returning?:boolean}
export default function FileBrowser({ client, subject, selectedInstance, preparedId, inline=false, editScope, transfer, onClose }: { client: Client; subject: Ref; selectedInstance?: ExecInstance; preparedId?: string; inline?:boolean;editScope?:string;transfer?:FilesTransfer; onClose: () => void }) {
  // The inspector belongs to the object/container it opened on, not a later
  // background selection. Reopening Files resolves a fresh provider default.
  const leave=(go:()=>void)=>editScope?mayLeaveMatching(go,h=>h.scope===editScope):mayLeave(go)
  const [initial]=useState(transfer?.initial)
  const [source] = useState(subject)
  const [container, setContainer] = useState<Container | null>(()=>initial?.container??(preparedId?{instance:'',channel:'',title:source.title||source.name}:null))
  const [treeWidth, setTreeWidth] = useState(initial?.treeWidth??306)
  const [directories, setDirectories] = useState<Directories>(initial?.directories??{})
  const directoryCache = useRef<Directories>(initial?.directories??{})
  const inFlight = useRef(new Map<string, Promise<FilesResponse>>())
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set(initial?.expanded??['/']))
  const [selected, setSelected] = useState(initial?.selected??'/')
  const [directory, setDirectory] = useState(initial?.directory??'/')
  const [address, setAddress] = useState(initial?.address??'/')
  const [documentEpoch,setDocumentEpoch]=useState(initial?.documentEpoch??0)
  const [doc, setDoc] = useState<FilesResponse | null>(initial?.doc??null)
  const [text, setText] = useState(initial?.text??'')
  const [error, setError] = useState(initial?.error??'')
  const [initializing, setInitializing] = useState(!initial)
  const [busy, setBusy] = useState(false)
  const [readingPath, setReadingPath] = useState('')
  const [resolvingLink, setResolvingLink] = useState(false)
  const [writing, setWriting] = useState(false)
  const [downloading, setDownloading] = useState(false)
  const [downloaded, setDownloaded] = useState(initial?.downloaded??'')
  const [deleteSource,setDeleteSource]=useState<{path:string;version:string}|null>(null)
  const [renameSource,setRenameSource]=useState<{path:string;version:string}|null>(null)
  const [createDirectory,setCreateDirectory]=useState<string|null>(null)
  const [saved, setSaved] = useState(initial?.saved??false)
  const [language, setLanguage] = useState(initial?.language??'')
  const [languages, setLanguages] = useState<LanguageDescription[]>([])
  const state = useRef({ doc, text, writing })
  useEffect(() => { state.current = { doc, text, writing } }, [doc, text, writing])
  useEffect(()=>editScope&&!transfer?.managed?holdDockClose(editScope,()=>state.current.writing):undefined,[editScope,transfer?.managed])
  const editorSession=useRef(initial?.editor)
  const lastSnapshot=useRef<FilesSnapshot|undefined>(initial)
  const revision = useRef(initial?.revision??'')
  const box = useRef<HTMLElement>(null)
  const [mark] = useState(focusMark)
  useEffect(() => { box.current?.focus(); return () => restoreFocus(mark) }, [mark])
  const alive = useRef(true)
  const sequence = useRef(0)
  useEffect(() => { alive.current = true; return () => { alive.current = false } }, [])
  useEffect(() => {
    if(!preparedId||transfer?.managed)return
    // StrictMode's discarded mount must not revoke the real mount's session.
    return () => {setTimeout(()=>{if(!alive.current)void client.files({ref:source,preparedId,instance:'',channel:'',command:'close',path:'/'}).catch(()=>{})},0)}
  },[client,preparedId,source,transfer?.managed])
  useEffect(() => holdEdits({
    scope:editScope,
    retainedOnUnload:()=>!!transfer?.returning&&!state.current.writing,
    dirty: () => state.current.writing || (!!state.current.doc && state.current.text !== state.current.doc.text),
    discard: () => { setDoc(null); setText('') },
    focus: () => box.current?.querySelector<HTMLElement>('.cm-content')?.focus(),
  }), [editScope,transfer?.returning])

  useEffect(() => {
    let live = true
    void import('@codemirror/language-data').then(module => { if (live) setLanguages(module.languages) })
    if(preparedId||initial)return()=>{live=false}
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
  }, [client, source, selectedInstance, preparedId, initial])

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
    const request = client.files({ ref: source, preparedId, instance: container.instance, channel: container.channel, command: 'list', path, configRev: revision.current })
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
  }, [client, container, source, preparedId, updateDirectory])

  useEffect(() => {
    if (!container||initial) return
    const timer = setTimeout(() => {
      void loadDirectory('/').catch(() => {}).finally(() => { if (alive.current) setInitializing(false) })
    }, 0)
    return () => clearTimeout(timer)
  }, [container, loadDirectory, initial])

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
      const result = await client.files({ ref: source, preparedId, instance: container.instance, channel: container.channel, command, path, configRev: revision.current,
        ...(command === 'write' ? { text: state.current.text, expect: state.current.doc?.version } : {}) })
      if (!alive.current || current !== sequence.current) return
      revision.current = result.configRev
      setDoc(result); setText(result.text); setSelected(result.path)
      if (command === 'read') {setLanguage('');setDocumentEpoch(previous=>previous+1)}
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
        const result = await client.files({ ref: source, preparedId, instance: container.instance, channel: container.channel, command:'resolve', path, configRev:revision.current })
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
    else leave(() => void follow())
  }
  const download = async (path: string) => {
    if (!container || downloading) return
    setDownloading(true); setDownloaded(''); setError('')
    try {
      const destination = await downloadContainerFiles({ ref: source, preparedId, instance: container.instance, channel: container.channel, path, configRev: revision.current, command: 'read' })
      if (alive.current && destination) setDownloaded(destination)
    } catch (failure) { if (alive.current) setError(errorText(failure)) }
    finally { if (alive.current) setDownloading(false) }
  }
  const createFile=async(name:string)=>{
    if(!container||busy)throw new Error(t('app.loading'))
    const parent=createDirectory??directory,path=(parent==='/'?'':parent)+'/'+name
    setBusy(true);setWriting(true);setError('')
    try{
      const result=await client.files({ref:source,preparedId,instance:container.instance,channel:container.channel,command:'create',path,configRev:revision.current})
      if(!alive.current)return
      revision.current=result.configRev
      const cached=directoryCache.current[parent]
      updateDirectory(parent,{...cached,entries:[...(cached?.entries??[]).filter(item=>item.name!==name),{name,directory:false,symlink:false,target:''}]})
      setExpanded(previous=>new Set([...previous,parent]));setDirectory(parent);setAddress(parent);setSelected(path)
      setDoc(result);setText('');setLanguage('');setDocumentEpoch(previous=>previous+1);setSaved(false);setCreateDirectory(null)
    }finally{if(alive.current){setBusy(false);setWriting(false)}}
  }
  const prepareRename=(path:string)=>{
    if(!container||busy||Object.values(directoryCache.current).some(item=>item.loading))return
    leave(()=>{void(async()=>{
      setBusy(true);setError('')
      try{
        const info=await client.files({ref:source,preparedId,instance:container.instance,channel:container.channel,command:'rename-info',path,configRev:revision.current})
        if(alive.current&&info.version)setRenameSource({path,version:info.version})
      }catch(e){if(alive.current)setError(errorText(e))}finally{if(alive.current)setBusy(false)}
    })()})
  }
  const renameFile=async(name:string)=>{
    if(!container||busy||!renameSource)throw new Error(t('app.loading'))
    const sourcePath=renameSource.path,parent=sourcePath.slice(0,sourcePath.lastIndexOf('/'))||'/',oldName=sourcePath.slice(sourcePath.lastIndexOf('/')+1)
    setBusy(true);setWriting(true);setError('')
    try{
      const result=await client.files({ref:source,preparedId,instance:container.instance,channel:container.channel,command:'rename',path:sourcePath,newName:name,expect:renameSource.version,configRev:revision.current})
      if(!alive.current)return
      const remap=(path:string)=>path===sourcePath||path.startsWith(sourcePath+'/')?result.path+path.slice(sourcePath.length):path
      const next:Directories={}
      const staleDestination=(path:string)=>path===result.path||path.startsWith(result.path+'/')
      for(const [path,entry]of Object.entries(directoryCache.current)){
        if(staleDestination(path))continue
        next[remap(path)]=path===parent?{...entry,entries:entry.entries?.filter(item=>item.name!==name).map(item=>item.name===oldName?{...item,name}:item)}:entry
      }
      directoryCache.current=next;setDirectories(next)
      setExpanded(previous=>new Set([...previous].filter(path=>!staleDestination(path)).map(remap)));setDirectory(remap);setAddress(remap);setSelected(result.path)
      if(state.current.doc){
        if(staleDestination(state.current.doc.path)){setDoc(null);setText('')}
        else setDoc({...state.current.doc,path:remap(state.current.doc.path)})
      }
      revision.current=result.configRev;setRenameSource(null);setSaved(false)
    }finally{if(alive.current){setBusy(false);setWriting(false)}}
  }
  const prepareDelete=(path:string)=>{
    if(!container||busy||path==='/'||Object.values(directoryCache.current).some(item=>item.loading))return
    leave(()=>{void(async()=>{
      setBusy(true);setError('')
      try{
        const info=await client.files({ref:source,preparedId,instance:container.instance,channel:container.channel,command:'delete-info',path,configRev:revision.current})
        if(alive.current&&info.version)setDeleteSource({path,version:info.version})
      }catch(e){if(alive.current)setError(errorText(e))}finally{if(alive.current)setBusy(false)}
    })()})
  }
  const deleteFile=async()=>{
    if(!container||busy||!deleteSource)throw new Error(t('app.loading'))
    const path=deleteSource.path,parent=path.slice(0,path.lastIndexOf('/'))||'/',name=path.slice(path.lastIndexOf('/')+1)
    const removed=(value:string)=>value===path||value.startsWith(path+'/')
    setBusy(true);setWriting(true);setError('')
    try{
      const result=await client.files({ref:source,preparedId,instance:container.instance,channel:container.channel,command:'delete',path,expect:deleteSource.version,configRev:revision.current})
      if(!alive.current)return
      const next:Directories={}
      for(const [key,entry]of Object.entries(directoryCache.current))if(!removed(key))next[key]=key===parent?{...entry,entries:entry.entries?.filter(item=>item.name!==name)}:entry
      directoryCache.current=next;setDirectories(next);setExpanded(previous=>new Set([...previous].filter(key=>!removed(key))))
      setSelected(parent);setDirectory(parent);setAddress(parent)
      if(state.current.doc&&removed(state.current.doc.path)){setDoc(null);setText('')}
      revision.current=result.configRev;setDeleteSource(null);setSaved(false)
    }finally{if(alive.current){setBusy(false);setWriting(false)}}
  }
  const openFile = (path: string) => { if (!busy) leave(() => void requestFile('read', path)) }
  const close = () => { if (!writing) {if(inline)onClose();else leave(onClose)} }
  const transferBusy=busy||writing||downloading||resolvingLink||createDirectory!==null||renameSource!==null||deleteSource!==null||Object.values(directories).some(d=>d.loading)
  useLayoutEffect(()=>{const snapshot:FilesSnapshot={container,treeWidth,directories,expanded:[...expanded],selected,directory,address,doc,text,error,revision:revision.current,language,saved,downloaded,busy:transferBusy,writing,initializing,documentEpoch,editor:editorSession.current};lastSnapshot.current=snapshot;transfer?.capture?.(snapshot)})
  const autoLanguage = doc ? LanguageDescription.matchFilename(languages, doc.path)?.name : undefined
  const directoryLoading = Object.values(directories).some(item => item.loading)

  const content = <>
    <div data-file-overlay={inline?undefined:true} className={inline?"h-full min-h-0":"fixed inset-0 z-30 flex items-center justify-center bg-black/50 p-[12px]"} onMouseDown={event => { if (event.target === event.currentTarget) close() }}>
      <section data-file-browser ref={box} inert={createDirectory!==null||renameSource!==null||deleteSource!==null} tabIndex={-1} role={inline?"region":"dialog"} aria-modal={inline?undefined:true} aria-label={t('files.title')}
        className={inline?"relative flex h-full min-h-0 w-full flex-col bg-panel":"relative flex h-[calc(100dvh-24px)] w-[min(1800px,calc(100vw-24px))] flex-col border border-line bg-panel"}
        onKeyDownCapture={event=>{
          if(transfer?.onWindowAction&&toolWindowShortcut(event.nativeEvent)){
            event.preventDefault();event.stopPropagation()
            if(!event.repeat&&!transferBusy&&!initializing)transfer.onWindowAction()
          }
        }}
        onKeyDown={event => {
          if (!inline && event.key === 'Tab') {
            const controls = [...(box.current?.querySelectorAll<HTMLElement>('button:not(:disabled):not([tabindex="-1"]), input:not(:disabled), [tabindex="0"], [contenteditable="true"]') ?? [])]
              .filter(item => item.getClientRects().length > 0 && !item.closest('[inert]'))
            const first = controls[0], last = controls.at(-1)
            if (event.shiftKey && (document.activeElement === first || document.activeElement === box.current)) { event.preventDefault(); last?.focus() }
            else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
          }
          if (!inline && event.key === 'Escape') { event.stopPropagation(); close() }
          if (!event.altKey && isShortcut(event, 'KeyS', { ctrl: true, shift: false })) { event.preventDefault(); if (doc && !busy) void requestFile('write', doc.path) }
        }}>
        {!inline&&<header className="flex h-[35px] shrink-0 items-center gap-3 border-b border-line px-3 py-[4px]" data-file-header>
          <strong>{t('files.title')}</strong>
          <span className="min-w-0 flex-1 truncate text-sm text-fg-muted" data-file-context title={container?.title}>{container?.title ?? source.name}</span>
          <button className={button} disabled={writing} onClick={close} aria-label={t('drawer.close')}>×</button>
        </header>}
        <div className="flex min-h-[35px] shrink-0 flex-wrap items-center border-b border-line" data-file-toolbar>
          <div className="flex min-w-[210px] max-w-full grow basis-full items-center gap-2 px-3 py-[4px] sm:max-w-[65%] sm:grow-0 sm:basis-auto" style={{width:treeWidth}} data-file-address>
            <form className="flex min-w-0 flex-1 gap-2" onSubmit={event => { event.preventDefault(); void revealDirectory(address) }}>
              <input disabled={initializing || writing} aria-label={t('files.path')} className="h-[26px] min-w-0 flex-1 border border-line bg-app px-2 font-mono text-[13px]"
                value={address} onChange={event => setAddress(event.target.value)} />
              <button className={button} disabled={initializing || writing}>{t('files.go')}</button>
            </form>
            <ActionIconButton className="h-[26px] w-[26px] shrink-0" label={t('files.create')} description={t('files.create')} disabled={initializing||busy||writing||directoryLoading} onClick={()=>leave(()=>setCreateDirectory(directory))}><ActionIcon name="newFile"/></ActionIconButton>
            <RefreshButton className="h-[26px] w-[26px] shrink-0" busy={!!directories[directory]?.refreshing} disabled={initializing || writing || !!directories[directory]?.loading} onClick={() => refreshDirectory(directory)} />
          </div>
          <div className="flex min-w-0 flex-1 items-center gap-2 px-3 py-[4px]" data-file-document-header={doc?true:undefined}>
            <span className="min-w-0 flex-1 truncate font-mono text-sm" title={doc?.path}>{doc?.path}{doc&&text !== doc.text ? ' *' : ''}</span>
            {doc&&<button className={button} disabled={busy || text === doc.text} onClick={() => void requestFile('write', doc.path)}>{t('files.save')}</button>}
            {transfer?.onWindowAction&&<ActionIconButton className="h-[26px] w-[26px] shrink-0" disabled={transferBusy||initializing} label={t(transfer.returning?'logs.returnWindow':'logs.detachWindow')} description={`${t(transfer.returning?'logs.returnWindow':'logs.detachWindow')} (Alt+Shift+Enter)`} onClick={transfer.onWindowAction}><WindowActionIcon returning={transfer.returning} className="h-4 w-4"/></ActionIconButton>}
          </div>
        </div>
        {error && <div role="alert" data-file-error className="absolute right-3 bottom-[39px] z-30 flex max-w-[min(600px,calc(100%-24px))] items-start gap-3 border border-danger/50 bg-panel px-3 py-2 text-sm text-danger shadow-lg">
          <p className="max-h-40 min-w-0 overflow-auto whitespace-pre-wrap break-words">{error}</p>
          <button type="button" className="flex h-6 w-6 shrink-0 items-center justify-center hover:bg-hover" aria-label={t('files.dismissError')}
            onClick={() => { setError(''); box.current?.focus({preventScroll:true}) }}>×</button>
        </div>}
        <div className="flex min-h-0 flex-1">
          <div className="relative min-h-0 shrink-0" style={{width:treeWidth,maxWidth:'65%'}} data-file-tree-panel>
          <FileTree directories={directories} expanded={expanded} selected={selected} disabled={writing || initializing || downloading || resolvingLink} onSelect={(path, folder) => { setSelected(path); const parent = folder ? path : path.slice(0, path.lastIndexOf('/')) || '/'; setDirectory(parent); setAddress(parent) }}
            canRename={!busy&&!directoryLoading} onRename={prepareRename} onDelete={prepareDelete} onToggle={toggleDirectory} onOpen={openFile} onRefresh={refreshDirectory} onDownload={path => void download(path)} onFollowLink={followLink} />
          <PanelResize label={t('files.resizeTree')} outside value={treeWidth} min={140} max={() => (box.current?.clientWidth ?? 1200)*0.65} onDone={setTreeWidth} />
          </div>
          <div className="relative flex min-w-0 flex-1 flex-col" data-file-editor-panel aria-busy={!!readingPath || undefined}>
            {doc ? <>
              <div className="min-h-0 flex-1" inert={busy}>
                <Suspense fallback={<div className="flex h-full items-center justify-center"><Loading /></div>}><Editor key={`${doc.path}:${documentEpoch}`} filename={doc.path} language={language} text={text} session={initial?.editor?.path===doc.path&&initial.editor.epoch===documentEpoch?initial.editor.session:undefined} onSession={session=>{const current=lastSnapshot.current;if(current?.doc?.path!==doc.path||current.documentEpoch!==documentEpoch)return;editorSession.current={path:doc.path,epoch:documentEpoch,session};transfer?.capture?.({...current,text:session.text,editor:editorSession.current})}} editable onChange={setText} label={doc.path} /></Suspense>
              </div>
            </> : !readingPath && <p className="m-auto text-sm text-fg-muted">{t('files.choose')}</p>}
            {readingPath && <div className="absolute inset-0 z-10 flex items-center justify-center bg-panel/90" data-file-read-loading><Loading label={t('files.loadingFile', {path:readingPath})} /></div>}
          </div>
        </div>
        <footer className="flex h-[27px] shrink-0 items-center gap-3 border-t border-line px-3 py-[2px] text-[12px] leading-[18px] text-fg-muted" data-file-footer>
          {inline&&<span className="min-w-0 max-w-[50%] shrink truncate" data-file-context title={container?.title}>{container?.title ?? source.name}</span>}
          <span className="min-w-0 flex-1 truncate" aria-live="polite">{busy || initializing || directoryLoading || downloading || resolvingLink ? t('app.loading') : downloaded ? t('files.downloaded', { path: downloaded }) : saved ? t(preparedId?'files.savedHost':'files.saved') : t('files.limit')}</span>
          {doc && <Select label={t('files.language')} className="file-footer-select text-[12px]" value={language} options={[
            { value: '', label: `${autoLanguage ?? t('files.plain')} · ${t('files.auto')}` },
            { value: 'plain', label: t('files.plain') }, ...languages.map(item => ({ value: item.name, label: item.name })),
          ]} onChange={setLanguage} />}
        </footer>
      </section>
    </div>
    {deleteSource&&<FileDeleteDialog path={deleteSource.path} onDelete={deleteFile} onClose={()=>setDeleteSource(null)}/>}
    {createDirectory!==null&&<FileNameDialog directory={createDirectory} onSubmit={createFile} onClose={()=>setCreateDirectory(null)}/>}
    {renameSource&&<FileNameDialog mode="rename" directory={renameSource.path.slice(0,renameSource.path.lastIndexOf('/'))||'/'} initialName={renameSource.path.slice(renameSource.path.lastIndexOf('/')+1)} onSubmit={renameFile} onClose={()=>setRenameSource(null)}/>}
    </>
  return inline?content:createPortal(content,document.body)
}
