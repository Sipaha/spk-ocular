import { beforeAll, describe, expect, it } from 'vitest'
import { providerEnglish } from './test/providerSource.mjs'
import { _dicts, _providerTexts, detailLabel, loadLanguage, messageText, setLanguage, t } from './i18n'
import { languages } from './languages'

const params = (value: string) => value.match(/\{\w+\}/g)?.sort() ?? []
const numbers = (value: string) => value.replaceAll('−', '-').replace(/(?<=\d)[ ,.\u00a0\u202f](?=\d{3}\b)/g, '').match(/-?\d+(?:\.\d+)?/g)?.sort() ?? []
beforeAll(async () => { await Promise.all(languages.map(loadLanguage)) })

describe('i18n', () => {
  it('has the same keys in every language', () => {
    for (const language of languages) {
      const dictionary = _dicts[language]!
      expect(Object.keys(dictionary).sort(), language).toEqual(Object.keys(_dicts.en).sort())
      for (const [key, value] of Object.entries(dictionary)) {
        expect(value.trim(), `${language}:${key}`).not.toBe('')
        expect(params(value), `${language}:${key}`).toEqual(params(_dicts.en[key as keyof typeof _dicts.en]))
      }
    }
  })
  it('translates every provider message with its exact placeholders', () => {
    for (const language of languages.filter(l => l !== 'en')) {
      const dictionary = _providerTexts[language]!
      expect(Object.keys(dictionary).sort(), language).toEqual(Object.keys(providerEnglish).sort())
      for (const [key, value] of Object.entries(dictionary)) {
        expect(value.trim(), `${language}:${key}`).not.toBe('')
        expect(params(value), `${language}:${key}`).toEqual(params(providerEnglish[key]))
      }
    }
  })
  it('keeps message parameters as data in every language', () => {
    for (const language of languages) {
      setLanguage(language)
      const raw = '{name} $& <script> test'
      expect(t('action.failed', { class: 'error', detail: raw })).toContain(raw)
      expect(messageText({ key: 'kubernetes.error.unknown', params: { kind: 'deployment', name: 'web', detail: raw }, text: raw })).toContain(raw)
    }
    setLanguage('en')
  })
  it('preserves literal numerical limits and technical identifiers in additional languages', () => {
    const identifiers = ['SIGKILL', 'SIGTERM', 'AutoRemove', 'OnDelete', 'Recreate', 'PodDisruptionBudget', 'startingDeadlineSeconds', 'securityContext', 'lookup', 'serviceAccountName', 'KUBECONFIG']
    for (const language of languages.filter(l => l !== 'en' && l !== 'ru')) {
      for (const [source, translated] of [[_dicts.en, _dicts[language]!], [providerEnglish, _providerTexts[language]!]] as [Record<string, string>, Record<string, string>][]) {
        for (const [key, value] of Object.entries(source)) {
          expect(numbers(translated[key]), `${language}:${key}`).toEqual(numbers(value))
          for (const identifier of identifiers.filter(name => value.includes(name))) {
            expect(translated[key], `${language}:${key}`).toContain(identifier)
          }
        }
      }
    }
  })
  it('does not advertise unchanged English sentences as additional-language translations', () => {
    for (const language of languages.filter(l => l !== 'en' && l !== 'ru')) {
      for (const [source, translated] of [[_dicts.en, _dicts[language]!], [providerEnglish, _providerTexts[language]!]] as [Record<string, string>, Record<string, string>][]) {
        for (const [key, value] of Object.entries(source)) {
          // Formatting-only templates and standard technical labels are valid.
          const words = value.replace(/\{\w+\}/g, '').match(/\b[A-Za-z]{2,}\b/g) ?? []
          if (words.length >= 3) expect(translated[key], `${language}:${key}`).not.toBe(value)
          if (language === 'ja' && words.length && !['Kubeconfig YAML', 'Chart', 'Kubeconfig', 'TLS', 'YAML'].includes(value)) {
            expect(translated[key], `${language}:${key}`).not.toBe(value)
          }
        }
      }
    }
  })
  it('substitutes variables and falls back for unknown detail keys', () => {
    setLanguage('ru')
    expect(t('sidebar.problems', { count: 2 })).toBe('Не удалось прочитать файлов: 2')
    expect(detailLabel('server')).toBe('Сервер')
    expect(detailLabel('something-new')).toBe('something-new')
    setLanguage('en')
  })
})

describe('messageText', () => {
  it('fills the template once: values are data', () => {
    setLanguage('ru')
    try {
      const say = (detail: string, kind = 'deployment', name = 'web') =>
        messageText({ key: 'kubernetes.error.unknown', params: { detail, kind, name }, text: 'en' })
      expect(say('server says {name} was rejected')).toBe('итог неизвестен (server says {name} was rejected): проверьте deployment web, прежде чем повторять')
      expect(say('{kind} {detail}', '{name}')).toBe('итог неизвестен ({kind} {detail}): проверьте {name} web, прежде чем повторять')
      for (const raw of ["$& $1 $` $' $$", '{"kind":"Status","message":"a \\"q\\" 100%"}', 'line1\nline2 %s %d']) {
        expect(say(raw)).toContain(`(${raw})`)
      }
      expect(messageText({ key: 'kubernetes.done.restart', params: { kind: 'deployment' }, text: 'en' })).toBe('deployment {name}: перезапуск запрошен')
      expect(messageText({ key: 'api.sessionClosed', text: 'en' })).toBe('сессия закрылась тем временем — попробуйте снова')
      expect(messageText({ text: 'only english' })).toBe('only english')
    } finally {
      setLanguage('en')
    }
  })
})

describe('t', () => {
  it('fills its own placeholders once: a detail is data', () => {
    expect(t('action.failed', { class: 'x', detail: 'said {class} and $&' })).toBe('Failed · x: said {class} and $&')
    expect(t('action.conflict', { detail: '{detail}' })).toBe('The object or the context changed since this was reviewed: {detail}')
  })
})
