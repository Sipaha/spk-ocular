import { describe, expect, it } from 'vitest'
import { englishKeyboardQuery } from './keyboardLayout'

describe('English keyboard layout recovery', () => {
  it('recovers a whole Russian type/name query and preserves case', () => {
    expect(englishKeyboardQuery('зщв ьщвуд')).toBe('pod model')
    expect(englishKeyboardQuery('ЗЩВ ЬЩВУД')).toBe('POD MODEL')
    expect(englishKeyboardQuery('сщтеуте')).toBe('content')
  })

  it('maps every physical letter key and shifted punctuation', () => {
    expect(englishKeyboardQuery('ёйцукенгшщзхъфывапролджэячсмитьбю')).toBe('`qwertyuiop[]asdfghjkl;\'zxcvbnm,.')
    expect(englishKeyboardQuery('ЁЙЦУКЕНГШЩЗХЪФЫВАПРОЛДЖЭЯЧСМИТЬБЮ')).toBe('~QWERTYUIOP{}ASDFGHJKL:"ZXCVBNM<>')
  })

  it('supports compatible Ukrainian letter positions', () => {
    expect(englishKeyboardQuery('іукмшсу')).toBe('service')
    expect(englishKeyboardQuery('ґієї')).toBe('`s\']')
    expect(englishKeyboardQuery('ҐІЄЇ')).toBe('~S"}')
  })

  it('leaves Latin names, separators and unrelated scripts alone', () => {
    expect(englishKeyboardQuery('pod ьщвуд-123 /v1')).toBe('pod model-123 /v1')
    for (const input of ['', 'pod model', '模型', 'モデル', 'écran', 'ß']) expect(englishKeyboardQuery(input)).toBeNull()
  })

  it('does not rewrite explicit commands or invent a command prefix', () => {
    expect(englishKeyboardQuery(':pod ьщвуд')).toBeNull()
    expect(englishKeyboardQuery('  :ns ыщьу')).toBeNull()
    expect(englishKeyboardQuery('Жте *')).toBeNull()
  })
})
