import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { Select, type SelectOption } from './Select'

const opts: SelectOption[] = [
  { value: 'a', label: 'alpha' },
  { value: 'b', label: 'beta', disabled: true },
  { value: 'c', label: 'gamma' },
  { value: 'd', label: 'delta' },
]

function Host({ onEsc, onChange }: { onEsc: () => void; onChange: (v: string) => void }) {
  const [v, setV] = useState('a')
  return (
    <div role="dialog" aria-label="box" onKeyDown={(e) => e.key === 'Escape' && onEsc()}>
      <Select
        label="Pick"
        value={v}
        options={opts}
        onChange={(x) => {
          setV(x)
          onChange(x)
        }}
      />
    </div>
  )
}

describe('Select', () => {
  it('keyboard: arrows skip disabled options, a letter jumps, Home/End, Enter chooses; Esc stays its own', async () => {
    const onEsc = vi.fn()
    const onChange = vi.fn()
    render(<Host onEsc={onEsc} onChange={onChange} />)
    const button = screen.getByRole('button', { name: 'Pick' })
    expect(button).toHaveTextContent('alpha')
    button.focus()
    await userEvent.keyboard('{ArrowDown}')
    const list = screen.getByRole('listbox', { name: 'Pick' })
    expect(list).toHaveFocus()
    const marked = () => within(list).getByRole('option', { selected: true }).textContent?.replace('✓', '')
    expect(marked()).toBe('alpha')
    await userEvent.keyboard('{ArrowDown}')
    expect(marked()).toBe('gamma') // beta is disabled
    await userEvent.keyboard('d')
    expect(marked()).toBe('delta')
    await userEvent.keyboard('{Home}')
    expect(marked()).toBe('alpha')
    await userEvent.keyboard('{End}')
    expect(marked()).toBe('delta')
    // A click on a disabled option does nothing.
    await userEvent.click(within(list).getByRole('option', { name: 'beta' }))
    expect(onChange).not.toHaveBeenCalled()
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
    expect(onEsc).not.toHaveBeenCalled() // the dialog around is not closed
    expect(button).toHaveFocus()
    await userEvent.keyboard('{ArrowUp}{End}{Enter}')
    expect(onChange).toHaveBeenCalledWith('d')
    expect(button).toHaveTextContent('delta')
    expect(button).toHaveFocus()
    // Esc with the list closed is the dialog's.
    await userEvent.keyboard('{Escape}')
    expect(onEsc).toHaveBeenCalledTimes(1)
  })

  it('long lists search; Tab closes and moves on', async () => {
    const many = Array.from({ length: 20 }, (_, i) => ({ value: `v${i}`, label: `item-${i}` }))
    const onChange = vi.fn()
    render(
      <>
        <Select label="Many" value="v0" options={many} onChange={onChange} />
        <button>after</button>
      </>,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Many' }))
    expect(screen.getByRole('combobox', { name: 'Search' })).toHaveFocus()
    await userEvent.keyboard('-1')
    expect(within(screen.getByRole('listbox', { name: 'Many' })).getAllByRole('option')).toHaveLength(11) // 1, 10–19
    await userEvent.keyboard('7{Enter}')
    expect(onChange).toHaveBeenCalledWith('v17')
    await userEvent.click(screen.getByRole('button', { name: 'Many' }))
    await userEvent.tab()
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  })

  it('stays open while something beside it scrolls (live logs); scrolling its own box closes it', async () => {
    render(
      <>
        <div data-testid="box" style={{ overflow: 'auto' }}>
          <Select label="Pick" value="a" options={opts} onChange={() => {}} />
        </div>
        <div data-testid="logs" style={{ overflow: 'auto' }} />
      </>,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Pick' }))
    fireEvent.scroll(screen.getByTestId('logs'))
    expect(screen.getByRole('listbox', { name: 'Pick' })).toBeInTheDocument()
    fireEvent.scroll(screen.getByTestId('box'))
    expect(screen.queryByRole('listbox', { name: 'Pick' })).not.toBeInTheDocument()
  })
})
