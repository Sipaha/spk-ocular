import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AgentAuditEntry, AgentPending, KindDescriptor } from '../api/types'
import { App } from '../App'
import { initialState, useStore } from '../store'
import { fakeClient, k8s, kindsView, podsKind } from '../test/fakeClient'
import { initialAgents, useAgents } from './store'

beforeEach(() => {
  useStore.setState({ ...initialState })
  useAgents.setState({ ...initialAgents })
  document.title = 'SPK Ocular'
})

const deployments: KindDescriptor = {
  id: 'apps/deployments',
  title: 'Deployments',
  group: 'Workloads',
  scoped: true,
  logs: true,
  editable: true,
  columns: [{ id: 'name', title: 'Name', type: 'text' }],
  actions: [
    { id: 'restart', title: 'Restart' },
    { id: 'delete', title: 'Delete', destructive: true },
  ],
}
const secrets: KindDescriptor = { id: 'secrets', title: 'Secrets', group: 'Config', scoped: true, editable: true, sensitive: true, columns: [] }
const nodes: KindDescriptor = { id: 'nodes', title: 'Nodes', group: 'Cluster', scoped: false, columns: [] }

async function setup(opts: { targets?: string[]; select?: boolean } = {}) {
  const f = fakeClient((opts.targets ?? ['prod', 'dev']).map((id) => k8s(id)))
  f.client.listKinds = vi.fn(async () => kindsView([{ ...podsKind, logs: true, actions: [{ id: 'delete', title: 'Delete', destructive: true }] }, deployments, secrets, nodes]))
  if (opts.select !== false) f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  render(<App client={f.client} />)
  await screen.findByRole('button', { name: 'Agents' })
  return { f, user: userEvent.setup() }
}

async function openPanel(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: 'Agents' }))
  return screen.getByRole('dialog', { name: 'Agent access' })
}

const editorOf = (panel: HTMLElement, title: string) => within(panel).getByRole('region', { name: title })

async function addScope(user: ReturnType<typeof userEvent.setup>, editor: HTMLElement, label: string | RegExp) {
  await user.click(await within(editor).findByRole('button', { name: 'Add' }))
  await user.click(screen.getByRole('option', { name: label }))
}

const pending = (id: string, extra: Partial<AgentPending> = {}): AgentPending => {
  const ref = { provider: 'kubernetes', target: 'prod', scope: 'web', kind: 'apps/deployments', name: `api-${id}` }
  return {
    id,
    agent: 'claude',
    at: `2026-10-01T00:00:0${id.slice(-1)}Z`,
    expires: new Date(Date.now() + 9 * 60_000).toISOString(),
    provider: 'kubernetes',
    target: 'prod',
    targetTitle: 'prod',
    ref,
    action: {
      where: { provider: 'kubernetes', target: 'prod', targetTitle: 'prod', endpoint: 'https://prod.example:6443', ref },
      action: { id: 'delete', title: 'Delete', destructive: true },
      params: {},
      destructive: true,
      effects: [{ text: `Deletes deployment api-${id} and its pods.` }],
      lists: [{ title: { text: 'Pods to be deleted' }, destructive: true, items: [{ name: `api-${id}-1` }] }],
      rights: { state: 'allowed' },
      expect: 'x',
    },
    ...extra,
  }
}

describe('agent access: grants', () => {
  it('shows the socket and the instruction line; a target\'s grants are saved whole', async () => {
    const { f, user } = await setup()
    const panel = await openPanel(user)
    expect(await within(panel).findByText(/Agents connect over \/home\/u\/.spk\/ocular\/agent.sock/)).toBeInTheDocument()
    expect(within(panel).getByText(/curl -s --unix-socket ~\/.spk\/ocular\/agent.sock/)).toBeInTheDocument()

    // The selected target is edited first.
    const editor = editorOf(panel, 'prod')
    expect(within(editor).getByText('Nothing is granted: agents see nothing of this target.')).toBeInTheDocument()
    await addScope(user, editor, 'web')
    const web = within(editor).getByRole('region', { name: 'Namespace web' })
    expect(within(web).getByRole('checkbox', { name: 'Read' })).toBeChecked()
    await user.click(within(web).getByRole('checkbox', { name: 'Logs' }))
    await user.click(within(web).getByRole('checkbox', { name: 'Restart' }))
    await user.click(within(editor).getByRole('button', { name: 'Save' }))
    expect(f.client.saveAgentGrants).toHaveBeenCalledWith('kubernetes', 'prod', [
      { scope: { mode: 'one', name: 'web' }, verb: 'read', kinds: null },
      { scope: { mode: 'one', name: 'web' }, verb: 'logs', kinds: null },
      { scope: { mode: 'one', name: 'web' }, verb: 'action:restart', kinds: null },
    ])
    // Saved: the list says so, the button waits for the next change.
    expect(await within(panel).findByText('granted: 3')).toBeInTheDocument()
    expect(within(editor).getByRole('button', { name: 'Save' })).toBeDisabled()

    // Another target is its own: nothing of prod's shows there.
    await user.click(within(panel).getByRole('button', { name: /^dev/ }))
    expect(within(editorOf(panel, 'dev')).getByText('Nothing is granted: agents see nothing of this target.')).toBeInTheDocument()
    await user.click(within(panel).getByRole('button', { name: /^prod/ }))
    await user.click(within(editorOf(panel, 'prod')).getByRole('button', { name: 'Revoke all of this target' }))
    expect(f.client.saveAgentGrants).toHaveBeenLastCalledWith('kubernetes', 'prod', [])
  })

  it('a destructive action cannot be saved for all kinds: the button says why; named kinds can', async () => {
    const { f, user } = await setup()
    const panel = await openPanel(user)
    const editor = editorOf(panel, 'prod')
    await addScope(user, editor, 'web')
    const web = within(editor).getByRole('region', { name: 'Namespace web' })
    await user.click(within(web).getByRole('checkbox', { name: 'Delete (destructive)' }))
    // Its kinds are chosen first.
    const picker = within(web).getByRole('group', { name: 'Kinds' })
    const save = within(editor).getByRole('button', { name: 'Save' })
    expect(save).toBeDisabled()
    expect(within(editor).getByRole('status')).toHaveTextContent('Cannot save: web · Delete: choose at least one kind')
    await user.click(within(picker).getByRole('checkbox', { name: 'All kinds' }))
    expect(within(editor).getByRole('status')).toHaveTextContent('Cannot save: web · Delete: a destructive action is granted only for kinds named — choose them')
    expect(save).toBeDisabled()
    // "Without confirmation" applies to kinds named only: not offered for all.
    expect(within(web).queryByRole('checkbox', { name: 'without confirmation' })).not.toBeInTheDocument()
    await user.click(within(picker).getByRole('checkbox', { name: 'All kinds' }))
    await user.click(within(picker).getByRole('checkbox', { name: 'Deployments' }))
    expect(save).toBeEnabled()
    await user.click(within(web).getByRole('checkbox', { name: 'without confirmation' }))
    expect(within(editor).getByRole('list', { name: 'warnings' })).toHaveTextContent('Some destructive plans will run without your confirmation.')
    await user.click(save)
    expect(f.client.saveAgentGrants).toHaveBeenCalledWith('kubernetes', 'prod', [
      { scope: { mode: 'one', name: 'web' }, verb: 'read', kinds: null },
      { scope: { mode: 'one', name: 'web' }, verb: 'action:delete', kinds: ['apps/deployments'], noConfirm: true },
    ])
  })

  it('warns of all namespaces, the cluster, edit with logs and operators; editing Secrets is by name only', async () => {
    const { user } = await setup()
    const panel = await openPanel(user)
    const editor = editorOf(panel, 'prod')
    await addScope(user, editor, 'All namespaces (later ones too)')
    await addScope(user, editor, /^Cluster/)
    const warnings = within(editor).getByRole('list', { name: 'warnings' })
    expect(warnings).toHaveTextContent('“All namespaces” covers every one, later ones too (kube-system included).')
    expect(warnings).toHaveTextContent('Objects outside namespaces name any of them')
    const cluster = within(editor).getByRole('region', { name: /^Cluster/ })
    expect(within(cluster).getAllByRole('checkbox').map((c) => c.getAttribute('aria-label') ?? c.parentElement?.textContent)).toEqual(['Read'])

    const all = within(editor).getByRole('region', { name: 'All namespaces (later ones too)' })
    await user.click(within(all).getByRole('checkbox', { name: 'Edit YAML' }))
    await user.click(within(all).getByRole('checkbox', { name: 'Logs' }))
    expect(warnings).toHaveTextContent('Editing workloads together with logs is about access to the namespace\'s Secrets')
    expect(warnings).toHaveTextContent('Editing resources an operator acts on')
    await user.click(within(all).getByRole('button', { name: 'Edit YAML: Kinds' }))
    const picker = within(all).getByRole('group', { name: 'Kinds' })
    // "All kinds" does not cover Secrets for edit.
    const secretsBox = within(picker).getByRole('checkbox', { name: /Secrets/ })
    expect(secretsBox).not.toBeChecked()
    expect(within(picker).getByText('by name only')).toBeInTheDocument()
    await user.click(within(picker).getByRole('checkbox', { name: 'All kinds' }))
    await user.click(within(picker).getByRole('checkbox', { name: /Secrets/ }))
    expect(warnings).toHaveTextContent('Editing a sensitive kind (Secret, ServiceAccount, RBAC) is granted')
  })

  it('Docker access is said to be about root', async () => {
    const f = fakeClient([])
    f.state.view.groups = [{ provider: 'compose', title: 'Docker Compose', targets: [{ provider: 'compose', id: 'local', title: 'local' }], problems: [] }]
    f.state.view.selected = { provider: 'compose', id: 'local' }
    f.client.listKinds = vi.fn(async () => kindsView([{ id: 'containers', title: 'Containers', group: 'Compose', scoped: true, columns: [] }]))
    f.client.listScopes = vi.fn(async () => ({ scopes: [{ name: 'shop' }] }))
    render(<App client={f.client} />)
    const user = userEvent.setup()
    const panel = await openPanel(user)
    const editor = editorOf(panel, 'local')
    await addScope(user, editor, 'shop')
    expect(within(editor).getByRole('list', { name: 'warnings' })).toHaveTextContent('Access to Docker is about root on this host')
  })

  it('a suspended target says what changed and is confirmed again', async () => {
    const { f, user } = await setup()
    f.state.agentTargets = [{ provider: 'kubernetes', target: 'prod', title: 'prod', identity: 'https://old:6443', observed: 'https://new:6443', grants: [{ scope: { mode: 'one', name: 'web' }, verb: 'read', kinds: null }] }]
    await act(async () => f.emit({ type: 'agent_grants_changed' }))
    const panel = await openPanel(user)
    expect(within(panel).getByText('suspended')).toBeInTheDocument()
    const editor = editorOf(panel, 'prod')
    expect(within(editor).getByRole('alert')).toHaveTextContent('The target changed: it was https://old:6443, now it is https://new:6443.')
    await user.click(within(editor).getByRole('button', { name: 'Confirm for what it is now' }))
    expect(f.client.reconfirmAgentTarget).toHaveBeenCalledWith('kubernetes', 'prod', 'https://new:6443')
    await waitFor(() => expect(within(panel).queryByText('suspended')).not.toBeInTheDocument())
  })

  it('a target gone from the configuration keeps its grants to revoke', async () => {
    const { f, user } = await setup()
    f.state.agentTargets = [{ provider: 'kubernetes', target: 'old', title: 'old-ctx', identity: 'x', grants: [{ scope: { mode: 'one', name: 'web' }, verb: 'read', kinds: null }] }]
    await act(async () => f.emit({ type: 'agent_grants_changed' }))
    const panel = await openPanel(user)
    await user.click(within(panel).getByRole('button', { name: /^old-ctx/ }))
    const editor = editorOf(panel, 'old-ctx')
    expect(within(editor).getByText('not in the configuration')).toBeInTheDocument()
    await user.click(within(editor).getByRole('button', { name: 'Revoke all of this target' }))
    expect(f.client.saveAgentGrants).toHaveBeenCalledWith('kubernetes', 'old', [])
  })

  it('"Revoke everything" asks first', async () => {
    const { f, user } = await setup()
    f.state.agentTargets = [{ provider: 'kubernetes', target: 'prod', title: 'prod', identity: 'x', grants: [{ scope: { mode: 'all' }, verb: 'read', kinds: null }] }]
    await act(async () => f.emit({ type: 'agent_grants_changed' }))
    const panel = await openPanel(user)
    await user.click(within(panel).getByRole('button', { name: 'Revoke everything' }))
    const ask = within(panel).getByRole('alertdialog', { name: 'Revoke every grant of every target?' })
    await user.click(within(ask).getByRole('button', { name: 'Cancel' }))
    expect(f.client.revokeAllAgentGrants).not.toHaveBeenCalled()
    await user.click(within(panel).getByRole('button', { name: 'Revoke everything' }))
    await user.click(within(panel).getByRole('button', { name: 'Revoke' }))
    expect(f.client.revokeAllAgentGrants).toHaveBeenCalled()
  })
})

describe('agent access: confirmation', () => {
  it('shows the plan as the agent saw it; No and Yes decide; a queue of two goes one by one; counters follow', async () => {
    const { f, user } = await setup()
    f.state.agentPending = [pending('p1'), pending('p2')]
    await act(async () => f.emit({ type: 'agent_pending_changed' }))
    const dialog = await screen.findByRole('dialog', { name: 'Agent “claude” asks you to confirm' })
    expect(dialog).toHaveTextContent('(not verified)')
    expect(dialog).toHaveTextContent('api-p1')
    expect(dialog).toHaveTextContent('Deletes deployment api-p1 and its pods.')
    expect(within(dialog).getByRole('region', { name: 'Pods to be deleted (1)' })).toHaveTextContent('api-p1-1')
    expect(dialog).toHaveTextContent('more waiting: 1')
    expect(dialog).toHaveTextContent(/expires in [89]:\d\d/)
    // Counters: the title, the status bar, the target in the list.
    expect(document.title).toBe('(2) SPK Ocular')
    expect(screen.getByRole('button', { name: 'Agents · waiting: 2' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Agents\' plans wait for your confirmation: 2' })).toBeInTheDocument()
    // The focus starts at No.
    expect(within(dialog).getByRole('button', { name: 'No' })).toHaveFocus()

    await user.click(within(dialog).getByRole('button', { name: 'No' }))
    expect(f.client.decideAgentPending).toHaveBeenCalledWith('p1', false)
    const next = await screen.findByRole('dialog', { name: 'Agent “claude” asks you to confirm' })
    await waitFor(() => expect(next).toHaveTextContent('api-p2'))
    // The next plan's dialog has the focus too (not given back behind it).
    expect(within(next).getByRole('button', { name: 'No' })).toHaveFocus()
    expect(document.title).toBe('(1) SPK Ocular')
    await user.click(within(next).getByRole('button', { name: 'Yes, run it' }))
    expect(f.client.decideAgentPending).toHaveBeenLastCalledWith('p2', true)
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /asks you to confirm/ })).not.toBeInTheDocument())
    expect(document.title).toBe('SPK Ocular')
    expect(screen.getByRole('button', { name: 'Agents' })).toBeInTheDocument()
  })

  it('decided elsewhere: the dialog moves on; Later puts it aside until the status bar brings it back', async () => {
    const { f, user } = await setup()
    f.state.agentPending = [pending('p1')]
    await act(async () => f.emit({ type: 'agent_pending_changed' }))
    const dialog = await screen.findByRole('dialog', { name: /asks you to confirm/ })
    await user.click(within(dialog).getByRole('button', { name: 'Later' }))
    expect(screen.queryByRole('dialog', { name: /asks you to confirm/ })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Agents · waiting: 1' }))
    expect(await screen.findByRole('dialog', { name: /asks you to confirm/ })).toBeInTheDocument()

    // Another window decided it.
    f.state.agentPending = []
    await act(async () => f.emit({ type: 'agent_pending_changed' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /asks you to confirm/ })).not.toBeInTheDocument())
  })

  it('an edit shows its diff', async () => {
    const { f } = await setup()
    const ref = { provider: 'kubernetes', target: 'prod', scope: 'web', kind: 'configmaps', name: 'cfg' }
    f.state.agentPending = [
      pending('e1', {
        ref,
        action: undefined,
        edit: { where: { provider: 'kubernetes', target: 'prod', targetTitle: 'prod', ref }, before: 'a: 1\nb: 2\n', after: 'a: 1\n', checked: true, changed: true, destructive: true, rights: { state: 'allowed' } },
      }),
    ]
    await act(async () => f.emit({ type: 'agent_pending_changed' }))
    const dialog = await screen.findByRole('dialog', { name: /asks you to confirm/ })
    expect(dialog).toHaveTextContent('Edit YAML')
    expect(within(dialog).getByRole('region', { name: 'Changes' })).toHaveTextContent('b: 2')
  })
})

describe('agent access: journal', () => {
  const entry = (id: number, extra: Partial<AgentAuditEntry> = {}): AgentAuditEntry => ({
    id,
    at: '2026-10-01T00:00:00Z',
    agent: id % 2 ? 'claude' : 'codex',
    method: 'ListObjects',
    provider: 'kubernetes',
    target: 'prod',
    scope: 'web',
    phase: 'read',
    outcome: 'done',
    count: 3,
    ...extra,
  })

  it('lists records newest first with the agent not verified; More reads older ones; filters by agent', async () => {
    const { f, user } = await setup()
    f.state.audit = Array.from({ length: 205 }, (_, i) => entry(205 - i))
    f.state.audit[0] = entry(205, { method: 'RunAction', phase: 'outcome', outcome: 'refused', verb: 'action:delete', destructive: true, object: 'apps/deployments/api', detail: 'forbidden: not granted', count: 1 })
    f.state.audit[2] = entry(203, { method: 'GetObject', scope: '*', object: '*', count: 10 })
    f.state.audit[3] = entry(202, { method: 'GetLogs', object: '*', count: 4 })
    const panel = await openPanel(user)
    await user.click(within(panel).getByRole('tab', { name: 'Journal' }))
    const table = await within(panel).findByRole('table', { name: 'Journal' })
    await waitFor(() => expect(within(table).getAllByRole('row')).toHaveLength(201))
    const first = within(table).getAllByRole('row')[1]
    expect(first).toHaveTextContent('claude (not verified)')
    expect(first).toHaveTextContent('RunAction · action:delete')
    expect(first).toHaveTextContent('prod · apps/deployments/api')
    expect(first).toHaveTextContent('forbidden: not granted')
    expect(first).toHaveTextContent('refused by the target')
    expect(within(table).getAllByRole('row')[2]).toHaveTextContent('prod · web')
    expect(within(table).getAllByRole('row')[2]).toHaveTextContent('read ×3')
    // Reads of several objects folded: not named as one of them.
    expect(within(table).getAllByRole('row')[3]).toHaveTextContent('prod · several scopes · several objects')
    expect(within(table).getAllByRole('row')[4]).toHaveTextContent('prod · web · several objects')
    await user.click(within(panel).getByRole('button', { name: 'More' }))
    await waitFor(() => expect(within(table).getAllByRole('row')).toHaveLength(206))
    expect(within(panel).queryByRole('button', { name: 'More' })).not.toBeInTheDocument()

    await user.click(within(panel).getByRole('button', { name: 'Agent' }))
    await user.click(screen.getByRole('option', { name: 'codex' }))
    await waitFor(() => expect(f.client.listAgentAudit).toHaveBeenLastCalledWith({ agent: 'codex', provider: undefined, target: undefined, limit: 200 }))
    await waitFor(() => expect(within(table).getAllByRole('row').slice(1).every((r) => r.textContent?.includes('codex'))).toBe(true))
  })
})
