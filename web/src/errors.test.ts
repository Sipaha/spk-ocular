import { afterEach, describe, expect, it } from 'vitest'
import { ApiError } from './api/client'
import { errorDetail } from './errors'
import { setLanguage } from './i18n'

afterEach(() => setLanguage('en'))

describe('errorDetail', () => {
  it('says the reason in the UI language, else the detail, else the code', () => {
    setLanguage('ru')
    const why = { key: 'api.configChanged', params: { target: 'prod' }, text: 'the configuration of prod changed' }
    expect(errorDetail(new ApiError('conflict', why.text, false, why))).toBe('конфигурация prod изменилась после просмотра — посмотрите снова')
    expect(errorDetail(new ApiError('conflict', 'server words'))).toBe('server words')
    expect(errorDetail(new ApiError('conflict', ''))).toBe('conflict')
    expect(errorDetail(new ApiError('conflict', 'd', false, { key: 'nobody.knows', text: 'english' }))).toBe('english')
  })
  it('any other rejection: its message or its text', () => {
    expect(errorDetail(new Error('boom'))).toBe('boom')
    expect(errorDetail('plain')).toBe('plain')
    expect(errorDetail(null)).toBe('null')
    expect(errorDetail(undefined)).toBe('undefined')
  })
})
