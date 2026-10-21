import {useCallback, useContext, useRef, useState} from 'react'
import type {Client} from '../api/client'
import type {LiveTarget, ManagementContext, Ref} from '../api/types'
import type {ArgoSnapshot} from '../argocd/types'
import type {FluxSnapshot} from '../fluxcd/types'
import {ArgoSources} from '../argocd/ArgoSources'
import {FluxSources} from '../fluxcd/FluxSources'
import {loadedLink} from '../fluxcd/model'
import {dock} from '../dock/store'
import {useStore} from '../store'
import {ViewConnectionContext} from '../views/useView'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {localDateTime} from '../dates'
import {errorDetail} from '../errors'
import {t} from '../i18n'
import {complete, identity, matchingControllers, validSubject} from './model'

const fluxState = (state: string) => t(state === 'Ready' ? 'flux.ready' : state === 'Reconciling' ? 'flux.reconciling' : state === 'Suspended' ? 'flux.suspended' : state === 'Failed' ? 'flux.failed' : 'argo.unknown')
const button = 'rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-40'
const currentTarget = (ref: Ref, view: ReturnType<typeof useStore.getState>['view']) => view?.groups.flatMap(g => g.targets).find(target => target.provider === ref.provider && target.id === ref.target)
const sourceOf = (ref: Ref) => {
 const target = currentTarget(ref, useStore.getState().view)
 return target?.connection?.state === 'connected' ? JSON.stringify([target.connection.id, target.configRev]) : null
}

/** Held reviews do not borrow current-workspace caches or automatically admit a
 * replacement connection. Namespace changes always require an explicit new load. */
export function ManagementReview(props: {client: Client; where: LiveTarget; context: ManagementContext}) {
 return <Review key={JSON.stringify([props.context, props.where.configRev])} {...props}/>
}
function Review({client, where, context}: {client: Client; where: LiveTarget; context: ManagementContext}) {
 const connection = useContext(ViewConnectionContext)
 const [initialConnection] = useState(connection)
 const view = useStore(s => s.view)
 const target = currentTarget(context.ref, view)
 const [origin] = useState(() => sourceOf(context.ref))
 const current = sourceOf(context.ref)
 const active = connection === initialConnection && !!origin && current === origin && target?.configRev === where.configRev && validSubject(context, where)
 const [namespace, setNamespace] = useState(context.fluxTracking?.candidates[0]?.namespace ?? context.ref.scope ?? '')
 const [admitted, setAdmitted] = useState<string | null>(null)
 const loadButton = useRef<HTMLButtonElement>(null)
 const [pinError, setPinError] = useState(false)
 const validNamespace = namespace.length <= 63 && /^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$/.test(namespace)
 const refJSON = JSON.stringify(context.ref)
 const hasArgo = !!context.argoTracking, hasFlux = !!context.fluxTracking
 const load = useCallback(async (signal: AbortSignal) => {
  const ref: Ref = JSON.parse(refJSON)
  const scope = {mode: 'one' as const, name: namespace}
  const [argo, flux] = await Promise.allSettled([
   hasArgo ? client.argoCD({provider: ref.provider, target: ref.target, scope}, signal) : Promise.resolve(undefined),
   hasFlux ? client.fluxCD({provider: ref.provider, target: ref.target, scope}, signal) : Promise.resolve(undefined),
  ])
  if (sourceOf(ref) !== origin) throw new Error(t('workspace.connectFirst'))
  return {argo, flux}
 }, [client, refJSON, namespace, hasArgo, hasFlux, origin])
 const request = useSnapshotRequest(load, JSON.stringify([context, where.configRev, origin, namespace]), {enabled: active && validNamespace && admitted === namespace})
 const result = !request.cancelled && active ? request.value : null
 const argo = result?.argo.status === 'fulfilled' ? result.argo.value : undefined
 const flux = result?.flux.status === 'fulfilled' ? result.flux.value : undefined
 const matches = matchingControllers(context, argo && {...argo, objects: argo.objects.filter(o => o.ref.scope === namespace)}, flux && {...flux, objects: flux.objects.filter(o => o.ref.scope === namespace)})
 const competing = matches.argo.length > 0 && matches.flux.length > 0
 const pin = (ref: Ref) => {
  if (!active || sourceOf(context.ref) !== origin || !ref.uid || ref.provider !== context.ref.provider || ref.target !== context.ref.target || ref.scope !== namespace) return
  setPinError(!dock.openProperties({provider: ref.provider, id: ref.target}, where.targetTitle, ref))
 }
 const link = (ref: Ref) => <button type="button" className={`${button} break-all text-left`} disabled={!active || request.busy} onClick={() => pin(ref)}>{t('management.pin')} · {ref.kind}/{ref.scope}/{ref.name}</button>
 return <section aria-label={t('management.title')} className="min-w-0 space-y-2 rounded border border-line p-3 text-xs">
  <h3 className="font-semibold">{t('management.title')}</h3>
  <p className="text-fg-muted">{t('management.notice')}</p>
  <p className="break-all font-mono text-fg-muted">UID {context.ref.uid} · RV {context.version} · {localDateTime(context.capturedAt)}</p>
  {context.argoTracking && <p className="break-all">{t('argo.tracking')}: {context.argoTracking.application} · {context.argoTracking.method}{context.argoTracking.installation ? ` · ${context.argoTracking.installation}` : ''}</p>}
  {context.fluxTracking && <p className="break-all">{t('flux.tracking')}: {context.fluxTracking.candidates.map(c => `${c.kind}/${c.namespace}/${c.name}${c.uid ? ` (${c.uid})` : ''}`).join(', ')}</p>}
  {context.truncated && <p className="text-warning">{t('argo.partial')}</p>}
  {!active && <p className="text-warning">{t('management.stale')}</p>}
  <div className="flex flex-wrap items-end gap-2">
   <label className="flex min-w-0 flex-col gap-1">{t('management.namespace')}<input value={namespace} disabled={!active || request.busy} onChange={e => {setNamespace(e.target.value); setAdmitted(null)}} onKeyDown={e => {if (e.key === 'Enter') {e.preventDefault(); e.stopPropagation()}}} className="w-52 max-w-full rounded border border-line bg-panel px-2 py-1" /></label>
   <button type="button" ref={loadButton} className={button} disabled={!active || !validNamespace} aria-disabled={request.busy || undefined} onClick={() => {if (!request.busy) {if (admitted === namespace) request.refresh(); else setAdmitted(namespace)}}}>{request.busy ? t('app.loading') : t('management.load')}</button>
   {request.busy && <button type="button" className={button} onClick={() => {request.cancel(); loadButton.current?.focus()}}>{t('view.loadCancel')}</button>}
  </div>
  {request.cancelled && <p>{t('view.loadCancelled')}</p>}
  {request.error != null && <p role="alert" className="text-danger">{errorDetail(request.error)}</p>}
  {result && <div className="space-y-3" aria-busy={request.busy}>
   {([['Argo CD', result.argo, argo], ['Flux', result.flux, flux]] as const).map(([title, settled, snapshot]) => <div key={title}>
    {settled.status === 'rejected' && <p role="alert" className="text-danger">{title}: {errorDetail(settled.reason)}</p>}
    {snapshot && <Coverage title={title} snapshot={snapshot}/>}
   </div>)}
   {competing && <p role="status" className="text-warning">{t('argo.unverifiedManager')} {t('flux.unverified')}</p>}
   {argo && (matches.argo.length === 1 ? matches.argo.map(o => <div key={identity(o.ref)} className="space-y-1 border-l-2 border-line pl-2">
    <p className="font-semibold">{t('argo.details')} · {o.ref.scope}/{o.ref.name}</p>{link(o.ref)}
    <p>{t('argo.sync')}: {o.sync || t('argo.unknown')} · {t('argo.health')}: {o.health || t('argo.unknown')}</p>
    <ArgoSources sources={o.sources} application/>
    {!!o.conditions.length && <div><h4 className="font-semibold">{t('flux.conditions')}</h4>{o.conditions.map((c,i) => <p key={i} className="whitespace-pre-wrap break-words">{c.type}: {c.message}</p>)}</div>}
   </div>) : <p>{t(matches.argo.length > 1 ? 'argo.ambiguousManager' : 'argo.unverifiedManager')}</p>)}
   {flux && (matches.flux.length === 1 ? matches.flux.map(o => <div key={identity(o.ref)} className="space-y-1 border-l-2 border-line pl-2">
    <p className="font-semibold">{o.kind} · {o.ref.scope}/{o.ref.name} · {fluxState(o.state)}</p>{link(o.ref)}
    <p className="break-words">{o.message}</p><p>{t('flux.generation')}: {o.observedGeneration}/{o.generation}</p>
    <FluxSources object={o}/>
    {!!o.conditions.length && <div><h4 className="font-semibold">{t('flux.conditions')}</h4>{o.conditions.map((c,i) => <p key={i} className="whitespace-pre-wrap break-words">{c.type}={c.status} · {c.reason}: {c.message} · {c.observedGeneration}</p>)}</div>}
    {o.links.map((reference,i) => {const source = loadedLink(reference, flux.objects, {mode:'one', name:namespace}, o.ref); return <div key={i} className="mt-2 border-l border-line pl-2"><p className="break-all">{reference.relation} · {reference.group}/{reference.kind}/{reference.namespace}/{reference.name}</p>{source && !source.truncated ? <>{link(source.ref)}<p>{fluxState(source.state)} · {source.message}</p><FluxSources object={source}/>{source.conditions.map((c,j) => <p key={j} className="break-words">{c.type}={c.status} · {c.reason}: {c.message}</p>)}</> : <p>{t('flux.unverified')}</p>}</div>})}
   </div>) : <p>{t('flux.unverified')}</p>)}
   <p className="text-fg-muted">{t('management.pinNotice')}</p>
  </div>}
  {pinError && <p role="alert" className="text-warning">{t('management.pinFailed')}</p>}
 </section>
}
function Coverage({title,snapshot}: {title:string;snapshot:ArgoSnapshot|FluxSnapshot}) {
 return <details><summary className={!complete(snapshot) ? 'text-warning' : 'text-fg-muted'}>{title} · {t('argo.coverage')} · {snapshot.discovery}{!complete(snapshot) ? ` · ${t('argo.partial')}` : ''} · {localDateTime(snapshot.capturedAt)}</summary><ul className="mt-1 space-y-1">{snapshot.coverage.map((c,i) => <li key={i} className="break-all">{c.kind} / {c.scope || '—'} · {c.state}</li>)}</ul></details>
}
