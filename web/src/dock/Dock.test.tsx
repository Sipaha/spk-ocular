import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import { initialState, useStore } from '../store'
import { kindsView, connectedClient as fakeClient, k8s, podRow, podsKind } from '../test/fakeClient'

// xterm needs a real layout: the tab body is a stand-in with the same
// focus target (a textarea inside [data-terminal]).
vi.mock('../term/TerminalView', () => ({
  default: ({ tab }: { tab: { open: { ref: { name: string }; instance?: string; channel?: string; command?: string[] } } }) => (
    <div data-terminal data-testid={`term-${tab.open.ref.name}`} data-instance={tab.open.instance ?? ''} data-channel={tab.open.channel ?? ''}>
      <textarea aria-label="terminal input" />
      {tab.open.command?.join(' ')}
    </div>
  ),
}))

beforeEach(() => useStore.setState({ ...initialState }))

const dockTabs = () => [...document.querySelectorAll<HTMLElement>('[data-tab-kind]')]

async function setup() {
  const f = fakeClient([k8s('prod'), k8s('dev')])
  f.client.listKinds = vi.fn(async () => kindsView([{ ...podsKind, logs: true, exec: true }]))
  f.state.rows = [podRow('api-1', 'web'), podRow('api-2', 'web')]
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  vi.stubGlobal('fetch', vi.fn(async () => new Response(new ReadableStream())))
  render(<App client={f.client} />)
  const grid = await screen.findByRole('grid', { name: 'resources' })
  return { f, grid, user: userEvent.setup() }
}

describe('terminal tabs', () => {
  it('S opens a terminal; another target keeps log tabs and terminals, which then show their context', async () => {
    const { grid, user } = await setup()
    await user.click(await within(grid).findByText('api-1'))
    await user.keyboard('s')
    await user.keyboard('l')
    await waitFor(() => expect(dockTabs()).toHaveLength(2))
    expect(dockTabs().map((t) => t.dataset.tabKind)).toEqual(['term', 'logs'])
    expect(within(dockTabs()[0]).queryByText('prod')).not.toBeInTheDocument() // current target: no badge

    await user.click(screen.getByRole('option', { name: /dev/ }))
    await screen.findByRole('heading', { name: 'Pods' })
    await waitFor(() => expect(within(dockTabs()[1]).getByText('prod')).toBeInTheDocument())
    expect(dockTabs().map((t) => t.dataset.tabKind)).toEqual(['term', 'logs']) // P18: recent targets stay open
    const term = dockTabs()[0]
    expect(within(term).getByText('prod')).toBeInTheDocument() // visible, not only a tooltip
    expect(screen.getByTestId('term-api-1')).toBeInTheDocument()
    // The badge and empty tab space activate it too; a larger close
    // button must not make the activation area depend on the title width.
    await user.click(within(term).getByText('prod'))
    expect(term).toHaveAttribute('aria-selected', 'true')
    const logs = dockTabs()[1]
    await user.click(logs)
    expect(logs).toHaveAttribute('aria-selected', 'true')
    await user.click(within(term).getByRole('button', { name: 'Close tab' }))
    expect(dockTabs()).toEqual([logs])
    expect(logs).toHaveAttribute('aria-selected', 'true')
    vi.unstubAllGlobals()
  })

  it("a log tab of another target keeps receiving lines", async () => {
    let ctl!: ReadableStreamDefaultController<Uint8Array>
    const { grid, user } = await setup()
    vi.stubGlobal('fetch', vi.fn(async () => new Response(new ReadableStream<Uint8Array>({ start: (c) => void (ctl = c) }))))
    await user.click(await within(grid).findByText('api-1'))
    await user.keyboard('l')
    await waitFor(() => expect(ctl).toBeDefined())
    await user.click(screen.getByRole('option', { name: /dev/ }))
    await waitFor(() => expect(within(dockTabs()[0]).getByText('prod')).toBeInTheDocument())
    const send = (f: unknown) => ctl.enqueue(new TextEncoder().encode(JSON.stringify(f) + '\n'))
    send({ k: 'source', id: 1, key: 'api-1/app', label: 'api-1/app', channel: 'app' })
    send({ k: 'lines', s: 1, l: [['2026-10-01T10:00:00Z', 'still streaming after the switch']] })
    send({ k: 'ready' })
    expect(await screen.findByText(/still streaming after the switch/)).toBeInTheDocument()
    vi.unstubAllGlobals()
  })

  it('Shift+S asks for the container and command and opens that', async () => {
    const { f, grid, user } = await setup()
    await user.click(await within(grid).findByText('api-2'))
    await user.keyboard('{Shift>}S{/Shift}')
    const dialog = await screen.findByRole('dialog', { name: 'Open a terminal' })
    await waitFor(() => expect(f.client.execInfo).toHaveBeenCalled())
    await user.type(within(dialog).getByRole('textbox', { name: /Command/ }), `psql -c 'select 1'`)
    await user.click(within(dialog).getByRole('button', { name: 'Open' }))
    expect(await screen.findByTestId('term-api-2')).toHaveTextContent('psql -c select 1')
    vi.unstubAllGlobals()
  })

  it('an instance without channels (a container) asks only for the instance', async () => {
    const { f, grid, user } = await setup()
    f.client.execInfo = vi.fn(async () => ({
      instances: [
        { id: 'c1', title: 'web-1', ready: true, channels: [], defaultChannel: '' },
        { id: 'c2', title: 'web-2', ready: false, channels: [], defaultChannel: '' },
      ],
      defaultInstance: 'c1',
      instanceLabel: { key: 'compose.level.container', text: 'Container' },
    }))
    await user.click(await within(grid).findByText('api-2'))
    await user.keyboard('{Shift>}S{/Shift}')
    const dialog = await screen.findByRole('dialog', { name: 'Open a terminal' })
    const pick = await within(dialog).findByRole('button', { name: 'Container' })
    expect(within(dialog).queryByRole('button', { name: 'Channel' })).not.toBeInTheDocument()
    // The app's own list, by keyboard: the next one, Enter.
    pick.focus()
    await user.keyboard('{ArrowDown}')
    expect(within(dialog).getByRole('option', { selected: true })).toHaveTextContent('web-1')
    await user.keyboard('{ArrowDown}{Enter}')
    expect(pick).toHaveTextContent('web-2 (not ready)')
    expect(pick).toHaveFocus()
    await user.click(within(dialog).getByRole('button', { name: 'Open' }))
    const term = await screen.findByTestId('term-api-2')
    expect(term).toHaveAttribute('data-instance', 'c2')
    expect(term).toHaveAttribute('data-channel', '')
    vi.unstubAllGlobals()
  })

  it('null lists from a provider do not break the dialog', async () => {
    const { f, grid, user } = await setup()
    const withNulls = [
      { instances: [{ id: 'c1', title: 'web-1', ready: true, channels: null, defaultChannel: '' }], defaultInstance: 'c1' },
      { instances: null, defaultInstance: '', noInstances: { key: 'compose.exec.noRunning', text: 'The service has no running containers' } },
    ]
    let current = withNulls[0]
    f.client.execInfo = vi.fn(async () => current as never)
    await user.click(await within(grid).findByText('api-2'))
    await user.keyboard('{Shift>}S{/Shift}')
    let dialog = await screen.findByRole('dialog', { name: 'Open a terminal' })
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Open' })).toBeEnabled())
    expect(within(dialog).queryByRole('button', { name: 'Channel' })).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    current = withNulls[1]
    await user.click(await within(grid).findByText('api-2'))
    await user.keyboard('{Shift>}S{/Shift}')
    dialog = await screen.findByRole('dialog', { name: 'Open a terminal' })
    expect(await within(dialog).findByText('The service has no running containers')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Open' })).toBeDisabled()
    vi.unstubAllGlobals()
  })

  it('an unclosed quote in the command is shown, nothing opens', async () => {
    const { grid, user } = await setup()
    await user.click(await within(grid).findByText('api-2'))
    await user.keyboard('{Shift>}S{/Shift}')
    const dialog = await screen.findByRole('dialog', { name: 'Open a terminal' })
    await user.type(within(dialog).getByRole('textbox', { name: /Command/ }), `echo 'oops`)
    await user.click(within(dialog).getByRole('button', { name: 'Open' }))
    expect(within(dialog).getByRole('alert')).toHaveTextContent('unclosed single quote')
    expect(screen.queryByTestId('term-api-2')).not.toBeInTheDocument()
    vi.unstubAllGlobals()
  })

  it('keys typed in a terminal do not reach app shortcuts', async () => {
    const { grid, user } = await setup()
    await user.click(await within(grid).findByText('api-1'))
    await user.keyboard('{Enter}') // the drawer
    const drawer = await screen.findByRole('dialog', { name: /api-1/ })
    await user.click(within(drawer).getByRole('button', { name: 'Terminal' }))
    const input = await screen.findByRole('textbox', { name: 'terminal input' })
    await user.click(input)
    await user.keyboard('/') // not the filter
    expect(screen.getByRole('textbox', { name: 'Filter rows' })).not.toHaveFocus()
    await user.keyboard('{Escape}') // not "close the drawer" (vim needs Escape)
    expect(screen.getByRole('dialog', { name: /api-1/ })).toBeInTheDocument()
    vi.unstubAllGlobals()
  })
})
