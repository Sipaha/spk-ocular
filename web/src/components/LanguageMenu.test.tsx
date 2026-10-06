import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setLanguage } from '../i18n'
import { initialState, useStore } from '../store'
import { fakeClient } from '../test/fakeClient'
import { LanguageMenu } from './LanguageMenu'

beforeEach(() => {
  setLanguage('en')
  useStore.setState({ ...initialState, info: { name: 'SPK Ocular', version: 'test', mode: 'browser', language: 'en', languagePreference: '' } })
})
afterEach(() => { cleanup(); setLanguage('en'); useStore.setState({ ...initialState }) })

describe('language menu', () => {
  it('offers native names and updates the durable preference without touching connections', async () => {
    const { client } = fakeClient([])
    render(<LanguageMenu client={client} />)
    fireEvent.click(screen.getByRole('button', { name: 'Language' }))
    for (const name of ['Русский', 'English', '简体中文', 'Español', 'Deutsch', 'Français', 'Português (Brasil)', '日本語']) {
      expect(screen.getByRole('option', { name })).toBeVisible()
    }
    expect(screen.getByRole('option', { name: '日本語' })).toHaveAttribute('lang', 'ja')
    expect(screen.getByRole('option', { name: 'Português (Brasil)' })).toHaveAttribute('lang', 'pt-BR')
    fireEvent.click(screen.getByRole('option', { name: 'Русский' }))
    await waitFor(() => expect(client.setLanguage).toHaveBeenCalledWith('ru'))
    await waitFor(() => expect(document.documentElement.lang).toBe('ru'))
    expect(useStore.getState().info?.languagePreference).toBe('ru')
    expect(client.connectTarget).not.toHaveBeenCalled()
    expect(client.closeTarget).not.toHaveBeenCalled()
  })
  it('keeps the previous choice when saving fails and lets the user retry', async () => {
    const { client } = fakeClient([])
    vi.mocked(client.setLanguage).mockRejectedValueOnce(new Error('disk unavailable'))
    render(<LanguageMenu client={client} />)
    fireEvent.click(screen.getByRole('button', { name: 'Language' }))
    fireEvent.click(screen.getByRole('option', { name: 'Русский' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Could not change language'))
    expect(document.documentElement.lang).toBe('en')
    expect(useStore.getState().info?.languagePreference).toBe('')
    fireEvent.click(screen.getByRole('button', { name: 'Language' }))
    fireEvent.click(screen.getByRole('option', { name: 'Русский' }))
    await waitFor(() => expect(document.documentElement.lang).toBe('ru'))
  })
  it('disables concurrent writes and leaves the current language while a write is pending', async () => {
    const { client } = fakeClient([])
    let finish!: () => void
    vi.mocked(client.setLanguage).mockImplementationOnce(() => new Promise<void>(resolve => { finish = resolve }))
    render(<LanguageMenu client={client} />)
    fireEvent.click(screen.getByRole('button', { name: 'Language' }))
    fireEvent.click(screen.getByRole('option', { name: 'Русский' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Language' })).toBeDisabled())
    expect(document.documentElement.lang).toBe('en')
    await act(async () => finish())
    expect(document.documentElement.lang).toBe('ru')
  })
})
