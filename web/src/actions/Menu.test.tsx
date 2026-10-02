import { fireEvent, render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { Menu } from './Menu'

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
