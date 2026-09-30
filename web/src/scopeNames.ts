// What the scopes of a provider are called (core.ScopeNames): the generic UI
// names no scope itself — Kubernetes says "Namespace", Compose "Project".
import type { ScopeNames, TargetsView } from './api/types'
import { messageText, t } from './i18n'
import { useStore } from './store'

export interface ScopeWords {
  singular: string
  plural: string
  all: string
}

export function scopeWords(names: ScopeNames | undefined): ScopeWords {
  if (!names) return { singular: t('scope.generic.singular'), plural: t('scope.generic.plural'), all: t('scope.generic.all') }
  return { singular: messageText(names.singular), plural: messageText(names.plural), all: messageText(names.all) }
}

export function scopeWordsOf(view: TargetsView | null | undefined, providerID: string | undefined): ScopeWords {
  return scopeWords(view?.groups.find((g) => g.provider === providerID)?.scopeNames)
}

/** The scope words of a provider (by default: the selected target's). */
export function useScopeWords(providerID?: string): ScopeWords {
  const view = useStore((s) => s.view)
  return scopeWordsOf(view, providerID ?? view?.selected?.provider)
}
