import { render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import { setLanguage } from '../i18n'
import { languages } from '../languages'
import { LogContext } from './LogContext'

afterEach(() => setLanguage('en'))

it('identifies the log source without duplicating toolbar period controls in every language', () => {
  for (const language of languages) {
    setLanguage(language)
    const { container, unmount } = render(<LogContext connectionName="Production" subject={{ provider: 'kubernetes', target: 'dev', scope: 'enterprise-ecos24', kind: 'apps/deployments', name: 'ecos-content-app' }} />)
    expect(container).toHaveTextContent('enterprise-ecos24')
    expect(container).toHaveTextContent('ecos-content-app')
    expect(container).not.toHaveTextContent('logs.connection')
    expect(container.querySelector('time')).toBeNull()
    expect(container).not.toHaveTextContent('All time')
    unmount()
  }
})

it('distinguishes a project from unscoped containers and does not invent a start time', () => {
  const { rerender, container } = render(<LogContext connectionName="Production" subject={{ provider: 'compose', target: 'local', scope: 'shop', kind: 'services', name: 'api' }} />)
  expect(container).toHaveTextContent('Project: shop')
  expect(container).not.toHaveTextContent('Since:')
  expect(container.querySelector('time')).toBeNull()
  rerender(<LogContext connectionName="Production" subject={{ provider: 'compose', target: 'local', kind: 'containers', name: 'full-id', title: 'api-container' }} />)
  expect(container).not.toHaveTextContent('Project:')
  expect(screen.getByTitle('containers/full-id')).toHaveTextContent('api-container')
})

it('never exposes opaque target IDs or kubeconfig paths as connection names', () => {
 const subject = { provider: 'kubernetes', target: 'file:/home/user/.kube/config:admin@cluster', kind: 'pods', name: 'api' }
 const { container, rerender } = render(<LogContext connectionName="Production" subject={subject} />)
 expect(container).toHaveTextContent('Connection: Production')
 expect(container.innerHTML).not.toContain(subject.target)
 rerender(<LogContext subject={subject} />)
 expect(container).toHaveTextContent('Connection: —')
 expect(container.innerHTML).not.toContain(subject.target)
})
