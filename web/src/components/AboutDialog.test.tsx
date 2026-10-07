import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { Browser } from '@wailsio/runtime'
import { AboutDialog, localizedSiteURL } from './AboutDialog'
import { App } from '../App'
import { fakeClient, k8s } from '../test/fakeClient'
import { initialState, useStore } from '../store'
import { setLanguage } from '../i18n'
import { languages } from '../languages'

beforeEach(() => { setLanguage('en'); useStore.setState({ ...initialState }); vi.mocked(Browser.OpenURL).mockReset() })
afterEach(() => setLanguage('en'))

it('keeps the active workspace, displays the real version, traps focus and restores it', async () => {
  const target = k8s('prod')
  const f = fakeClient([target], true); f.state.view.selected = target
  render(<App client={f.client} />)
  const grid = await screen.findByRole('grid', { name: 'resources' })
  const trigger = screen.getByRole('button', { name: 'About SPK Ocular' })
  await userEvent.click(trigger)
  const dialog = screen.getByRole('dialog', { name: 'About SPK Ocular' })
  expect(dialog).toHaveAttribute('aria-modal', 'true')
  expect(dialog).toHaveTextContent(useStore.getState().info!.version)
  expect(screen.getByRole('grid', { name: 'resources' })).toBe(grid)
  expect(screen.getByRole('link', { name: 'About the author' })).toHaveAttribute('href', 'https://sipaha.github.io/about/en/')
  expect(screen.getByRole('button', { name: 'Close' })).toHaveFocus()
  await userEvent.keyboard('{Shift>}{Tab}{/Shift}')
  expect(screen.getByRole('link', { name: 'About the author' })).toHaveFocus()
  await userEvent.keyboard('{Tab}')
  expect(screen.getByRole('button', { name: 'Close' })).toHaveFocus()
  await userEvent.keyboard('{Escape}')
  expect(screen.queryByRole('dialog', { name: 'About SPK Ocular' })).not.toBeInTheDocument()
  expect(trigger).toHaveFocus()
  expect(screen.getByRole('grid', { name: 'resources' })).toBe(grid)
})

it('opens desktop links through Wails and reports an opening failure', async () => {
  const info = { name: 'SPK Ocular', version: '0.1.0-dev', mode: 'desktop' as const, language: 'en' as const }
  render(<AboutDialog info={info} onClose={vi.fn()} />)
  await userEvent.click(screen.getByRole('link', { name: 'About the author' }))
  expect(Browser.OpenURL).toHaveBeenCalledWith('https://sipaha.github.io/about/en/')
  vi.mocked(Browser.OpenURL).mockRejectedValueOnce(new Error('opening denied'))
  await userEvent.click(screen.getByRole('link', { name: 'Source code' }))
  await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Could not open the link: opening denied'))
})

it('uses explicit language URLs, including Russian when website storage is unavailable', () => {
  for (const language of languages) {
    for (const site of ['about', 'spk-ocular'] as const) {
      const url = new URL(localizedSiteURL(site, language))
      expect(url.origin).toBe('https://sipaha.github.io')
      expect(url.pathname).toBe(`/${site}/${language === 'ru' ? '' : language + '/'}`)
      expect(url.search).toBe(language === 'ru' ? '?lang=ru' : '')
    }
  }
})

it('ignores an external-link failure delivered after the dialog closes', async () => {
  let fail!: (error: Error) => void
  vi.mocked(Browser.OpenURL).mockImplementationOnce(() => new Promise((_, reject) => { fail = reject }))
  const ui = render(<AboutDialog info={{ name: 'SPK Ocular', version: 'test', mode: 'desktop', language: 'en' }} onClose={vi.fn()} />)
  await userEvent.click(screen.getByRole('link', { name: 'About the author' }))
  ui.unmount()
  await act(async () => fail(new Error('late failure')))
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})
