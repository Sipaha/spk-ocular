import { describe, expect, it } from 'vitest'
import { fuzzyScore } from './score'

const rank = (q: string, texts: string[]) =>
  texts
    .map((t) => ({ t, s: fuzzyScore(q, t) }))
    .filter((x) => x.s !== null)
    .sort((a, b) => b.s! - a.s!)
    .map((x) => x.t)

describe('fuzzyScore', () => {
  it('an empty query matches everything equally', () => {
    expect(fuzzyScore('', 'anything')).toBe(0)
    expect(fuzzyScore('  ', 'anything')).toBe(0)
  })

  it('letters out of order do not match', () => {
    expect(fuzzyScore('bw', 'web')).toBeNull()
    expect(fuzzyScore('webx', 'web')).toBeNull()
  })

  it('exact, then prefix, then a word start, then a substring, then scattered letters', () => {
    expect(rank('web', ['my-webhook', 'w-e-b', 'web-api', 'cobweb', 'web'])).toEqual(['web', 'web-api', 'my-webhook', 'cobweb', 'w-e-b'])
  })

  it('initials of words beat letters scattered inside one', () => {
    expect(rank('dp', ['dumpster', 'db-primary'])).toEqual(['db-primary', 'dumpster'])
  })

  it('shorter is better among equals', () => {
    expect(rank('api', ['api-gateway', 'api'])).toEqual(['api', 'api-gateway'])
    expect(rank('api', ['api-gateway-long', 'api-gw'])).toEqual(['api-gw', 'api-gateway-long'])
  })

  it('ignores case, Cyrillic included', () => {
    expect(fuzzyScore('WEB', 'web')).toBe(fuzzyScore('web', 'web'))
    expect(fuzzyScore('под', 'Поды')).not.toBeNull()
    expect(rank('ст', ['Место', 'Сервер', 'Стол'])).toEqual(['Стол', 'Место'])
  })
})
