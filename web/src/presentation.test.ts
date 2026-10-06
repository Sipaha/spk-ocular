import { afterEach, describe, expect, it } from 'vitest'
import { setLanguage } from './i18n'
import { columnLabel, groupLabel, kindLabel, presentationLabels } from './presentation'
import { languages } from './languages'

afterEach(() => setLanguage('en'))
describe('localized presentation and stable identity', () => {
  it('has a complete presentation catalogue in all languages', () => {
    for (const language of languages) {
      expect(Object.keys(presentationLabels[language]).sort()).toEqual(Object.keys(presentationLabels.en).sort())
      expect(Object.values(presentationLabels[language]).every(value => value.trim())).toBeTruthy()
    }
  })
  it('translates known display labels without renaming provider data', () => {
    setLanguage('ru')
    expect(columnLabel('Name')).toBe('Имя')
    expect(columnLabel('CPU')).toBe('CPU')
    expect(columnLabel('constructor')).toBe('constructor')
    expect(columnLabel('__proto__')).toBe('__proto__')
    expect(columnLabel('vendor.example/Custom')).toBe('vendor.example/Custom')
    expect(groupLabel('kubernetes', 'Workloads')).toBe('Рабочие нагрузки')
    expect(groupLabel('synthetic', 'Workloads')).toBe('Workloads')
    const kind = { id: 'projects', title: 'Projects', group: 'Compose', columns: [], scoped: false }
    expect(kindLabel('compose', kind)).toBe('Проекты')
    expect(kindLabel('synthetic', kind)).toBe('Projects')
    expect(kind).toEqual({ id: 'projects', title: 'Projects', group: 'Compose', columns: [], scoped: false })
  })
})
