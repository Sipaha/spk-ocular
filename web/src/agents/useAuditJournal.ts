import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { Client } from '../api/client'
import type { AgentAuditEntry, AgentAuditFilter } from '../api/types'
import { errorDetail } from '../errors'

type Filter = Pick<AgentAuditFilter, 'agent' | 'provider' | 'target'>
const PAGE = 200
const RELOAD_MS = 1000
interface Owner { key: string; client: Pick<Client, 'listAgentAudit'>; filter: Filter; live: boolean; entries: AgentAuditEntry[]; olderMore: boolean; paging: boolean; lastLoad: number }
interface Page { key: string; entries: AgentAuditEntry[]; more: boolean; loading: boolean; paging: boolean; error: string | null }
const empty = (key: string): Page => ({ key, entries: [], more: false, loading: true, paging: false, error: null })

/** One filter incarnation owns first-page and pagination successes and failures.
 * Same-filter refresh retains older pages; a different filter never relabels them. */
export function useAuditJournal(client: Pick<Client, 'listAgentAudit'>, filter: Filter, tick: number) {
 const key = JSON.stringify(filter)
 const request = useMemo(() => JSON.parse(key) as Filter, [key])
 const current = useRef<Owner | null>(null)
 const [page, setPage] = useState<Page>(() => empty(key))
 const [pageClient, setPageClient] = useState(client)
 const [agentsSeen, setAgentsSeen] = useState<string[]>([])
 if (page.key !== key || pageClient !== client) { setPage(empty(key)); setPageClient(() => client) }
 useLayoutEffect(() => {
  const owner: Owner = { key, client, filter: request, live: true, entries: [], olderMore: false, paging: false, lastLoad: 0 }
  current.current = owner
  return () => { owner.live = false; if (current.current === owner) current.current = null }
 }, [client, key, request])
 const remember = useCallback((entries: AgentAuditEntry[]) => setAgentsSeen(seen => [...new Set([...seen, ...entries.map(entry => entry.agent)])].sort()), [])
 useEffect(() => {
  const owner = current.current!
  let live = true
  const timer = setTimeout(() => {
   owner.lastLoad = Date.now()
   void client.listAgentAudit({ ...request, limit: PAGE }).then(entries => {
    if (!live || !owner.live || current.current !== owner) return
    const full = entries.length === PAGE
    const oldest = entries.at(-1)?.id ?? Infinity
    const keep = full ? owner.entries.filter(entry => entry.id < oldest) : []
    owner.entries = [...entries, ...keep]
    setPage({ key, entries: owner.entries, more: full && (!keep.length || owner.olderMore), loading: false, paging: owner.paging, error: null })
    remember(entries)
   }, failure => {
    if (!live || !owner.live || current.current !== owner) return
    setPage(state => ({ ...state, loading: false, error: errorDetail(failure) }))
   })
  }, Math.max(0, owner.lastLoad + RELOAD_MS - Date.now()))
  return () => { live = false; clearTimeout(timer) }
 }, [client, key, request, tick, remember])
 const loadMore = () => {
  const owner = current.current
  const before = owner?.entries.at(-1)?.id
  if (!owner?.live || owner.paging || !before) return
  owner.paging = true
  setPage(state => ({ ...state, paging: true }))
  void owner.client.listAgentAudit({ ...owner.filter, before, limit: PAGE }).then(entries => {
   if (!owner.live || current.current !== owner) return
   const known = new Set(owner.entries.map(entry => entry.id))
   owner.entries = [...owner.entries, ...entries.filter(entry => entry.id < before && !known.has(entry.id))]
   owner.olderMore = entries.length === PAGE
   setPage(state => ({ ...state, entries: owner.entries, more: owner.olderMore, error: null }))
   remember(entries)
  }, failure => {
   if (owner.live && current.current === owner) setPage(state => ({ ...state, error: errorDetail(failure) }))
  }).finally(() => {
   if (!owner.live || current.current !== owner) return
   owner.paging = false
   setPage(state => ({ ...state, paging: false }))
  })
 }
 return { ...(page.key === key ? page : empty(key)), agentsSeen, loadMore }
}
