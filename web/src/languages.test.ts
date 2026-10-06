import { describe, expect, it } from 'vitest'
import { detectLanguage, initialLanguage, matchLanguage } from './languages'

describe('language preferences', () => {
  it('honors the first supported browser choice and excludes Traditional Chinese', () => {
    expect(detectLanguage(['fa-IR', 'zh-Hant-TW', 'pt-BR', 'en-US'])).toBe('pt')
    expect(detectLanguage(['ja-JP', 'ru-RU'])).toBe('ja')
    expect(detectLanguage(['fr-CA', 'de-DE'])).toBe('fr')
    expect(detectLanguage(['fa-IR'])).toBe('en')
    expect(matchLanguage('zh_CN.UTF-8')).toBe('zh')
    expect(matchLanguage('zh-Hans')).toBe('zh')
    expect(matchLanguage('zh-Hans-TW')).toBe('zh')
    expect(matchLanguage('zh-HK')).toBeNull()
    expect(matchLanguage('rubbish')).toBeNull()
  })
  it('persisted choice wins; automatic desktop respects system and browser uses browser order', () => {
    const info = { mode: 'desktop', language: 'ru' as const, languagePreference: '' as const, systemLanguage: 'ru' as const }
    expect(initialLanguage(info, ['ja-JP'])).toBe('ru')
    expect(initialLanguage({ ...info, languagePreference: 'de' }, ['ja-JP'])).toBe('de')
    expect(initialLanguage({ ...info, mode: 'browser' }, ['ja-JP'])).toBe('ja')
    expect(initialLanguage({ ...info, systemLanguage: '' }, ['ja-JP'])).toBe('ja')
  })
})
