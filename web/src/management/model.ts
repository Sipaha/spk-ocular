import type { LiveTarget, ManagementContext, Ref } from '../api/types'
import type { ArgoSnapshot } from '../argocd/types'
import type { FluxSnapshot } from '../fluxcd/types'
import { managerMatches as argoMatches } from '../argocd/model'
import { managerMatches as fluxMatches } from '../fluxcd/model'

export const identity = (ref: Ref) => JSON.stringify([ref.provider, ref.target, ref.kind, ref.scope ?? '', ref.name, ref.uid ?? ''])
export const validSubject = (context: ManagementContext, where: LiveTarget) => !!context.ref.uid && identity(context.ref) === identity(where.ref)
export const complete = (snapshot: ArgoSnapshot | FluxSnapshot) => !snapshot.truncated && snapshot.discovery === 'ready' && snapshot.coverage.length > 0 && snapshot.coverage.every(c => ['ready', 'empty'].includes(c.state))

/** A partial inventory must not turn a single returned candidate into ownership. */
export function matchingControllers(context: ManagementContext, argo?: ArgoSnapshot, flux?: FluxSnapshot) {
 const resource = {ref: context.ref, argoTracking: context.argoTracking, fluxTracking: context.fluxTracking}
 return {
  argo: !context.truncated && argo && complete(argo) ? argoMatches(resource as Parameters<typeof argoMatches>[0], argo.objects.filter(o => !o.truncated && o.ref.kind === 'argoproj.io/applications')) : [],
  flux: !context.truncated && flux && complete(flux) ? fluxMatches(resource, flux.objects.filter(o => o.kind === 'Kustomization' && o.ref.kind === 'kustomize.toolkit.fluxcd.io/kustomizations' || o.kind === 'HelmRelease' && o.ref.kind === 'helm.toolkit.fluxcd.io/helmreleases')) : [],
 }
}
