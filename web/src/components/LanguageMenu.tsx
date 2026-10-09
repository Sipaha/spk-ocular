import { useState } from 'react'
import type { Client } from '../api/client'
import { loadLanguage, setLanguage, t } from '../i18n'
import { languageNames, languages } from '../languages'
import { useStore } from '../store'
import { Select } from './Select'
import { GlobeIcon } from './icons'

/** Changes text without remounting workspaces, editors or live sessions. */
export function LanguageMenu({ client }: { client: Client }) {
  const info = useStore(s => s.info)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(false)
  if (!info) return null
  const choose = async (value: string) => {
    const language = languages.find(l => l === value)
    if (!language) return
    setBusy(true)
    setError(false)
    try {
      // A failed chunk request or preference write leaves the current language intact.
      await loadLanguage(language)
      await client.setLanguage(language)
      setLanguage(language)
      useStore.setState(s => ({ info: s.info ? { ...s.info, language, languagePreference: language } : null }))
    } catch {
      setError(true)
    } finally {
      setBusy(false)
    }
  }
  return <div className="language-menu">
    <Select value={info.language}
      options={languages.map(value => ({ value, label: languageNames[value], lang: value === 'pt' ? 'pt-BR' : value }))}
      label={t('language.title')} search={false} className="language-trigger"
      leadingIcon={<GlobeIcon className="h-4 w-4 shrink-0" />}
      disabled={busy} onChange={value => void choose(value)} />
    {error && <span role="alert" className="language-error">{t('language.failed')}</span>}
  </div>
}
