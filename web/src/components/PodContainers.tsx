import type { Resource } from '../api/types'
import { columnLabel } from '../presentation'

const sections = [
  { title: 'Containers', prefixes: ['container '] },
  { title: 'Init Containers', prefixes: ['init container ', 'sidecar container '] },
  { title: 'Debug Containers', prefixes: ['debug container '] },
] as const

/** Existing provider fact keys stay intact for agents and other consumers. */
export function podContainerFacts(resource: Resource) {
  const groups = sections.map(section => ({ title: section.title, containers: [] as { name: string; image: string; sidecar: boolean }[] }))
  const facts = resource.facts.filter(fact => {
    if (resource.ref.provider !== 'kubernetes' || resource.ref.kind !== 'pods') return true
    for (const [index, section] of sections.entries()) {
      const prefix = section.prefixes.find(prefix => fact.key.startsWith(prefix) && fact.key.length > prefix.length)
      if (prefix) {
        groups[index].containers.push({ name: fact.key.slice(prefix.length), image: fact.value, sidecar: prefix === 'sidecar container ' })
        return false
      }
    }
    return true
  })
  return { facts, groups: groups.filter(group => group.containers.length > 0) }
}

export function PodContainers({ groups }: { groups: ReturnType<typeof podContainerFacts>['groups'] }) {
  return groups.map(group => <section key={group.title} aria-label={columnLabel(group.title)} data-pod-containers={group.title}>
    <h3 className="mb-2 text-[14px] font-semibold text-fg">{columnLabel(group.title)}</h3>
    <dl className="min-w-0 space-y-3">
      {group.containers.map(container => <div key={container.name} className="min-w-0">
        <dt className="text-[14px] font-medium text-fg [overflow-wrap:anywhere]">{container.name}{container.sidecar && <span className="ml-2 text-[12px] font-normal text-fg-muted">{columnLabel('Sidecar')}</span>}</dt>
        <dd className="mt-0.5 font-mono text-[13px] text-fg-muted [overflow-wrap:anywhere]">{container.image}</dd>
      </div>)}
    </dl>
  </section>)
}
