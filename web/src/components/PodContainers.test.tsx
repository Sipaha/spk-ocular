import { afterEach, expect, it } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { PodContainers, podContainerFacts } from './PodContainers'
import { setLanguage } from '../i18n'
import { languages } from '../languages'
import { columnLabel } from '../presentation'
import type { Resource } from '../api/types'
const resource: Resource = {
 ref: { provider: 'kubernetes', target: 'dev', kind: 'pods', name: 'db', uid: 'uid' },
 health: { state: 'ok' }, yaml: '', relations: [], facts: [
  { key: 'Node', value: 'worker' }, { key: 'container database', value: 'registry.example/postgres:17' },
  { key: 'container exporter', value: 'registry.example/exporter:v1' },
  { key: 'init container init-rm-lost-found', value: 'registry.example/busybox:latest' },
  { key: 'sidecar container proxy', value: 'proxy:2' }, { key: 'debug container debugger', value: 'debug:1' },
  { key: 'labels', value: 'app=db' },
 ],
}
afterEach(() => setLanguage('en'))
it('partitions Pod containers without changing provider data or mixing init sidecars into ordinary containers', () => {
 const before = JSON.stringify(resource)
 const result = podContainerFacts(resource)
 expect(result.facts).toEqual([resource.facts[0], resource.facts[6]])
 expect(result.groups.map(g => [g.title, g.containers.map(c => c.name)])).toEqual([
  ['Containers', ['database', 'exporter']], ['Init Containers', ['init-rm-lost-found', 'proxy']], ['Debug Containers', ['debugger']],
 ])
 render(<PodContainers groups={result.groups} />)
 expect(within(screen.getByRole('region', { name: 'Containers' })).queryByText('proxy')).toBeNull()
 expect(within(screen.getByRole('region', { name: 'Init Containers' })).getByText('Sidecar')).toBeVisible()
 expect(screen.getByText('registry.example/busybox:latest')).toBeVisible()
 expect(JSON.stringify(resource)).toBe(before)
})
it.each(['compose', 'synthetic'])('leaves container-like custom facts from %s alone', provider => {
 expect(podContainerFacts({ ...resource, ref: { ...resource.ref, provider } })).toEqual({ facts: resource.facts, groups: [] })
})
it('omits empty sections and retains unrecognized facts on other Kubernetes kinds', () => {
 expect(podContainerFacts({ ...resource, facts: [] }).groups).toEqual([])
 expect(podContainerFacts({ ...resource, ref: { ...resource.ref, kind: 'configmaps' } }).facts).toEqual(resource.facts)
})
it.each(languages)('localizes section labels in %s without translating names or images', language => {
 setLanguage(language)
 const { unmount } = render(<PodContainers groups={podContainerFacts(resource).groups} />)
 for (const title of ['Containers', 'Init Containers', 'Debug Containers']) expect(screen.getByRole('region', { name: columnLabel(title) })).toBeVisible()
 expect(screen.getByText('init-rm-lost-found')).toBeVisible()
 expect(screen.getByText('registry.example/postgres:17')).toBeVisible()
 unmount()
})
