import { useState } from 'react'
import type { Client } from '../api/client'
import { loadLanguage, setLanguage, t } from '../i18n'
import { detectLanguage, languageNames, languages } from '../languages'
import { useStore } from '../store'
import { Select } from './Select'

/** Changes text without remounting workspaces, editors or live sessions. */
export function LanguageMenu({ client }: { client: Client }) {
  const info = useStore(s => s.info)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(false)
  if (!info) return null
  const choose = async (value: string) => {
    setBusy(true)
    setError(false)
    const preference = languages.find(l => l === value) ?? ''
    const language = preference || (info.mode === 'desktop' && info.systemLanguage) || detectLanguage(navigator.languages)
    try {
      // A failed chunk request or preference write leaves the current language intact.
      await loadLanguage(language)
      await client.setLanguage(preference)
      setLanguage(language)
      useStore.setState(s => ({ info: s.info ? { ...s.info, language, languagePreference: preference } : null }))
    } catch {
      setError(true)
    } finally {
      setBusy(false)
    }
  }
  return <div className="language-menu">
    <Select value={info.languagePreference ?? info.language}
      options={[{ value: '', label: t('language.automatic') }, ...languages.map(value => ({ value, label: languageNames[value], lang: value === 'pt' ? 'pt-BR' : value }))]}
      label={t('language.title')} search={false} className="language-trigger"
      disabled={busy} onChange={value => void choose(value)} />
    {error && <span role="alert" className="language-error">{t('language.failed')}</span>}
  </div>
}
