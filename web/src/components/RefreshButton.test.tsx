import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { RefreshButton } from './RefreshButton'

describe('RefreshButton', () => {
  it('can retain modal keyboard focus and supersede a cancellable snapshot read', () => {
    const click = vi.fn()
    const { rerender } = render(<RefreshButton disableWhileBusy={false} onClick={click} />)
    const button = screen.getByRole('button', { name: 'Refresh' })
    button.focus()
    rerender(<RefreshButton disableWhileBusy={false} busy onClick={click} />)
    expect(button).toHaveFocus()
    expect(button).toHaveAttribute('aria-disabled', 'false')
    fireEvent.click(button)
    expect(click).toHaveBeenCalledTimes(1)
  })
  it('keeps a stable accessible name and spins for the complete busy interval', () => {
    const { rerender } = render(<RefreshButton busy />)
    const button = screen.getByRole('button', { name: 'Refresh' })
    expect(button).toBeDisabled()
    expect(button).toHaveAttribute('aria-busy', 'true')
    expect(button.querySelector('svg')).toHaveClass('animate-spin')
    expect(button).not.toHaveAttribute('title')
    rerender(<RefreshButton />)
    expect(button).toBeEnabled()
    expect(button).toHaveAttribute('aria-busy', 'false')
    expect(button.querySelector('svg')).not.toHaveClass('animate-spin')
  })
})
