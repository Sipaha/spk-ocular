import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import type { ScopeSel } from '../api/types'
import { holdEdits, keepEditing, leaveAsked, mayLeave } from '../edit/guard'
import { parseScope } from '../scopes'
import { ScopeSelect } from './ScopeSelect'
import { createSelectMemory } from './Select'

function Host() {
  const [value, setValue] = useState<ScopeSel>({ mode: 'one', name: 'team-a' })
  const [memory] = useState(createSelectMemory)
  return <>
    <ScopeSelect key={JSON.stringify(value)} value={value} memory={memory} names={['team-a', 'team-b', 'team-c']} label="Namespace" allLabel="All namespaces" onChange={(v) => mayLeave(() => setValue(v))} />
    <output data-testid="value">{JSON.stringify(value)}</output>
  </>
}
const button = () => screen.getByRole('button', { name: 'Namespace' })
const check = (name: string) => screen.getByRole('checkbox', { name })
const value = () => JSON.parse(screen.getByTestId('value').textContent!)

describe('multiple scopes', () => {
  it('checkboxes preserve search, scroll and focus across a scope-keyed page; text selects exactly one', async () => {
    render(<Host />)
    await userEvent.click(button())
    await userEvent.type(screen.getByRole('combobox'), 'team-')
    const list = screen.getByRole('listbox')
    list.scrollTop = 120
    fireEvent.scroll(list)
    await userEvent.click(check('team-b'))
    expect(value()).toEqual({ mode: 'some', names: ['team-a', 'team-b'] })
    expect(screen.getByRole('combobox')).toHaveValue('team-')
    expect(screen.getByRole('combobox')).toHaveFocus()
    expect(screen.getByRole('listbox').scrollTop).toBe(120)
    expect(check('team-a')).toBeChecked()
    expect(check('team-b')).toBeChecked()
    await userEvent.click(screen.getByText('team-b', { selector: 'span' }))
    expect(value()).toEqual({ mode: 'one', name: 'team-b' })
    expect(screen.queryByRole('listbox')).toBeNull()
  })

  it('unchecking the last scope yields an empty set; only All widens it, Enter chooses one', async () => {
    render(<Host />)
    await userEvent.click(button())
    await userEvent.click(check('team-a'))
    expect(value()).toEqual({ mode: 'some', names: [] })
    expect(button()).toHaveTextContent('Nothing selected')
    await userEvent.click(screen.getByRole('option', { name: 'All namespaces' }))
    expect(value()).toEqual({ mode: 'all' })
    await userEvent.click(button())
    await userEvent.click(check('team-c'))
    expect(value()).toEqual({ mode: 'one', name: 'team-c' })
    await userEvent.type(screen.getByRole('combobox'), 'team-b{Enter}')
    expect(value()).toEqual({ mode: 'one', name: 'team-b' })
    expect(screen.queryByRole('listbox')).toBeNull()
  })

  it('Space in the list toggles; Space in search types; Escape returns focus', async () => {
    render(<Host />)
    await userEvent.click(button())
    await userEvent.type(screen.getByRole('combobox'), 'team')
    screen.getByRole('listbox').focus()
    await userEvent.keyboard('{ArrowDown} ')
    expect(value()).toEqual({ mode: 'some', names: ['team-a', 'team-b'] })
    expect(screen.getByRole('listbox')).toHaveFocus()
    await userEvent.keyboard(' ')
    expect(value()).toEqual({ mode: 'one', name: 'team-a' })
    await userEvent.click(screen.getByRole('combobox'))
    await userEvent.keyboard(' ')
    expect(screen.getByRole('combobox')).toHaveValue('team ')
    await userEvent.keyboard('{Escape}')
    expect(button()).toHaveFocus()
  })

  it('closes before the discard question and retains selection if editing continues', async () => {
    render(<Host />)
    let release!: () => void
    act(() => { release = holdEdits({ dirty: () => true, discard: () => {} }) })
    try {
      await userEvent.click(button())
      await userEvent.click(check('team-b'))
      expect(leaveAsked()).toBe(true)
      expect(screen.queryByRole('listbox')).toBeNull()
      act(() => keepEditing())
      expect(value()).toEqual({ mode: 'one', name: 'team-a' })
      await userEvent.click(button())
      expect(check('team-a')).toBeChecked()
      expect(check('team-b')).not.toBeChecked()
    } finally { act(() => release()) }
  })

  it('restores canonical sets and rejects malformed saved selectors', () => {
    expect(parseScope({ mode: 'some', names: ['b', 'a', 'b'] })).toEqual({ mode: 'some', names: ['a', 'b'] })
    expect(parseScope({ mode: 'some', names: ['a', 'a'] })).toEqual({ mode: 'one', name: 'a' })
    expect(parseScope({ mode: 'some', names: [] })).toEqual({ mode: 'some', names: [] })
    expect(parseScope({ mode: 'some', names: [''] })).toBeNull()
    expect(parseScope({ mode: 'unexpected' })).toBeNull()
  })
})
