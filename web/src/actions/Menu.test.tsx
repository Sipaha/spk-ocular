import { fireEvent, render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { Menu } from './Menu'

it.each(['Enter', ' '])('menu activation with %j does not reach an ancestor shortcut', (key) => {
  const ancestor = vi.fn((event: React.KeyboardEvent) => event.preventDefault())
  render(<div onKeyDown={ancestor}><Menu items={[{ id: 'reset', label: 'Reset', onSelect: vi.fn() }]} at={{ x: 0, y: 0 }} label="Configurations" onClose={vi.fn()} /></div>)
  const item = screen.getByRole('menuitem', { name: 'Reset' })
  expect(fireEvent.keyDown(item, { key })).toBe(true)
  expect(ancestor).not.toHaveBeenCalled()
})

it('held Enter remains suppressed and a disabled item cannot activate', () => {
  const selected = vi.fn(), close = vi.fn(), ancestor = vi.fn()
  render(<div onKeyDown={ancestor}><Menu items={[{ id: 'reset', label: 'Reset', onSelect: selected, disabled: true }]} at={{ x: 0, y: 0 }} label="Configurations" onClose={close} /></div>)
  const item = screen.getByRole('menuitem', { name: 'Reset' })
  expect(fireEvent.keyDown(item, { key: 'Enter', repeat: true })).toBe(false)
  fireEvent.click(item)
  expect(selected).not.toHaveBeenCalled()
  expect(close).not.toHaveBeenCalled()
  expect(ancestor).not.toHaveBeenCalled()
})

it('pointer navigation moves keyboard focus to the hovered item', () => {
  const restart = vi.fn()
  const rollback = vi.fn()
  render(<Menu items={[{ id: 'restart', label: 'Restart', onSelect: restart }, { id: 'rollback', label: 'Roll back', onSelect: rollback }]} at={{ x: 0, y: 0 }} label="Actions" onClose={vi.fn()} />)
  const item = screen.getByRole('menuitem', { name: 'Roll back' })
  fireEvent.pointerMove(item)
  expect(item).toHaveFocus()
})

it('a parent update does not reset the active menu item', () => {
  const items = ['Restart', 'Scale', 'Roll back'].map((label) => ({ id: label, label, onSelect: vi.fn() }))
  const { rerender } = render(<Menu items={items} at={{ x: 0, y: 0 }} label="Actions" onClose={() => {}} />)
  fireEvent.keyDown(screen.getByRole('menuitem', { name: 'Restart' }), { key: 'ArrowDown' })
  const scale = screen.getByRole('menuitem', { name: 'Scale' })
  expect(scale).toHaveFocus()
  rerender(<Menu items={items} at={{ x: 0, y: 0 }} label="Actions" onClose={() => {}} />)
  expect(scale).toHaveFocus()
})
