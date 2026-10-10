import type { ClusterTimeline, TimelineEvent, TimelineResource } from '../api/types'

export interface TimelineRow {
 event: TimelineEvent
 root: TimelineResource
 chain: TimelineResource[]
 incomplete: boolean
}
export interface TimelineFilter {query: string; warnings: boolean; kind: string; rangeMs: number}

// Ownership uses immutable UID and the same namespace/target. A historical event
// about a replaced object must never join the replacement's workload lane.
export function timelineRows(snapshot: ClusterTimeline, filter: TimelineFilter): TimelineRow[] {
 const resources = new Map(snapshot.resources.map(r => [r.ref.uid, r]))
 const ancestry = new Map<string, {chain: TimelineResource[]; incomplete: boolean}>()
 const chainFor = (subject: TimelineEvent) => {
  const key = JSON.stringify([subject.subject.provider, subject.subject.target, subject.subject.scope, subject.subject.uid, subject.subject.kind, subject.subject.name])
  const cached = ancestry.get(key)
  if(cached) return cached
  const found = resources.get(subject.subject.uid)
  const valid = found && found.ref.provider === subject.subject.provider && found.ref.target === subject.subject.target && found.ref.scope === subject.subject.scope
  let node: TimelineResource = valid ? found : {ref: subject.subject, kindTitle: subject.subjectKind}
  const chain = [node], seen = new Set([node.ref.uid])
  let incomplete = !valid
  // Bounded independently of malformed ownership depth; no recursive stack.
  while(node.ownerUid) {
   const parent = resources.get(node.ownerUid)
   if(!parent || seen.has(node.ownerUid) || chain.length >= 32 || parent.ref.scope !== node.ref.scope || parent.ref.provider !== node.ref.provider || parent.ref.target !== node.ref.target) {incomplete = true; break}
   seen.add(node.ownerUid); chain.unshift(parent); node = parent
  }
  const result = {chain, incomplete}
  ancestry.set(key, result)
  return result
 }
 const query = filter.query.trim().toLocaleLowerCase()
 const since = filter.rangeMs ? snapshot.capturedAt - filter.rangeMs : 0
 const rows: TimelineRow[] = []
 for(const event of snapshot.events) {
  if(filter.warnings && event.type !== 'Warning') continue
  if(filter.kind && event.subjectKind !== filter.kind) continue
  if(since && (!event.lastAt || event.lastAt < since)) continue
  const {chain, incomplete} = chainFor(event)
  if(query && ![event.reason,event.message,event.subject.scope??'',...chain.flatMap(r=>[r.ref.title??r.ref.name,r.ref.name,r.kindTitle])].some(v=>v.toLocaleLowerCase().includes(query))) continue
  rows.push({event,root:chain[0],chain,incomplete})
 }
 return rows.sort((a,b) => (a.root.ref.scope??'').localeCompare(b.root.ref.scope??'') || a.root.kindTitle.localeCompare(b.root.kindTitle) || a.root.ref.name.localeCompare(b.root.ref.name) || (a.root.ref.uid??'').localeCompare(b.root.ref.uid??'') || a.event.lastAt - b.event.lastAt || a.event.id.localeCompare(b.event.id))
}

export function timelineBounds(rows: TimelineRow[], capturedAt: number, rangeMs: number) {
 let start = rangeMs ? capturedAt-rangeMs : capturedAt
 let end = capturedAt
 if(!rangeMs) for(const {event} of rows) {
  if(event.firstAt > 0) start = Math.min(start,event.firstAt)
  if(event.lastAt > 0) start = Math.min(start,event.lastAt)
 }
 for(const {event} of rows) if(event.lastAt > 0) end = Math.max(end,event.lastAt)
 if(end-start < 60000) start = end-60000
 return {start,end}
}

// Density counts event-series records at their last observation, not individual
// occurrences: cumulative counts cannot reconstruct missing occurrence times.
export function timelineDensity(rows: TimelineRow[], start: number, end: number) {
 const bins = Array.from({length:48},()=>({normal:0,warning:0}))
 for(const {event} of rows) {
  if(!event.lastAt) continue
  const index = Math.max(0,Math.min(bins.length-1,Math.floor((event.lastAt-start)/(end-start)*bins.length)))
  bins[index][event.type==='Warning'?'warning':'normal']++
 }
 return bins
}
