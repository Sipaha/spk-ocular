import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { LocationStatus, LocationStatusProvider, LocationStatusScope } from './LocationStatus'

afterEach(cleanup)

describe('location status ownership', () => {
  it('switches active locations without retaining hidden status or controls', () => {
    const UI = ({ logs }: { logs: boolean }) => <LocationStatusProvider>{slot => <>
      <LocationStatusScope active={!logs}><LocationStatus>Resources</LocationStatus></LocationStatusScope>
      <LocationStatus active={logs}><button>Cancel export</button>Live logs</LocationStatus>
      <footer ref={slot} data-testid="footer" />
    </>}</LocationStatusProvider>
    const view = render(<UI logs />)
    expect(screen.getByTestId('footer').textContent).toContain('Live logs')
    expect(screen.queryByText('Resources')).toBeNull()
    view.rerender(<UI logs={false} />)
    expect(screen.getByTestId('footer').textContent).toBe('Resources')
    expect(screen.queryByRole('button', { name: 'Cancel export' })).toBeNull()
    view.unmount()
    expect(screen.queryByText('Resources')).toBeNull()
  })

  it('keeps a local status in standalone viewers', () => {
    render(<LocationStatus>Detached logs</LocationStatus>)
    expect(screen.getByText('Detached logs').className).toContain('log-status')
  })
})
