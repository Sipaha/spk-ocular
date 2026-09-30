import { describe, expect, it } from 'vitest'
import { _dicts, detailLabel, messageText, setLanguage, t } from './i18n'

describe('i18n', () => {
  it('has the same keys in every language', () => {
    expect(Object.keys(_dicts.ru).sort()).toEqual(Object.keys(_dicts.en).sort())
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
