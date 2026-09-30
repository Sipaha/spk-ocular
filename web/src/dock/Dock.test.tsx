import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import { initialState, useStore } from '../store'
import { fakeClient, k8s, podRow, podsKind } from '../test/fakeClient'

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
  f.client.listKinds = vi.fn(async () => [{ ...podsKind, logs: true, exec: true }])
  f.state.rows = [podRow('api-1', 'web'), podRow('api-2', 'web')]
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  vi.stubGlobal('fetch', vi.fn(async () => new Response(new ReadableStream())))
  render(<App client={f.client} />)
  const grid = await screen.findByRole('grid', { name: 'resources' })
  return { f, grid, user: userEvent.setup() }
}

describe('terminal tabs', () => {
  it('S opens a terminal; another target closes log tabs but not terminals, which then show their context', async () => {
    const { grid, user } = await setup()
    await user.click(await within(grid).findByText('api-1'))
    await user.keyboard('s')
    await user.keyboard('l')
    await waitFor(() => expect(dockTabs()).toHaveLength(2))
    expect(dockTabs().map((t) => t.dataset.tabKind)).toEqual(['term', 'logs'])
    expect(within(dockTabs()[0]).queryByText('prod')).not.toBeInTheDocument() // current target: no badge

    await user.click(screen.getByRole('option', { name: /dev/ }))
    await screen.findByRole('heading', { name: 'Pods' })
    await waitFor(() => expect(dockTabs()).toHaveLength(1))
    const term = dockTabs()[0]
    expect(term).toHaveAttribute('data-tab-kind', 'term')
    expect(within(term).getByText('prod')).toBeInTheDocument() // visible, not only a tooltip
    expect(screen.getByTestId('term-api-1')).toBeInTheDocument()
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
    const pick = await within(dialog).findByRole('combobox', { name: 'Container' })
    expect(within(dialog).queryByRole('combobox', { name: 'Channel' })).not.toBeInTheDocument()
    await user.selectOptions(pick, 'c2')
    await user.click(within(dialog).getByRole('button', { name: 'Open' }))
    const term = await screen.findByTestId('term-api-2')
    expect(term).toHaveAttribute('data-instance', 'c2')
    expect(term).toHaveAttribute('data-channel', '')
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
