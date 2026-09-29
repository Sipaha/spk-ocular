import { describe, expect, it } from 'vitest'
import { _dicts, detailLabel, setLanguage, t } from './i18n'

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
