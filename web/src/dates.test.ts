import { expect, it } from 'vitest'
import { isoDateTime, localDate, localDateTime } from './dates'

it('uses local machine time, day-first order and a 24-hour clock', () => {
 const date = new Date(2026, 10, 5, 16, 7, 8)
 expect(localDate(date.getTime())).toBe('05.11.2026')
 expect(localDateTime(date.toISOString())).toBe('05.11.2026 16:07:08')
 expect(localDateTime('2026-11-05T16:07:08+03:00')).toBe(localDateTime(Date.UTC(2026,10,5,13,7,8)))
})
it('handles day rollover and daylight saving in the actual machine timezone', () => {
 const cases: Record<string,string> = {UTC:'05.11.2026 21:50:10','Asia/Novosibirsk':'06.11.2026 04:50:10','America/New_York':'05.11.2026 16:50:10'}
 const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
 if(cases[zone]) expect(localDateTime('2026-11-05T21:50:10Z')).toBe(cases[zone])
 const summer = new Date(2026,6,1,12,34,56), winter = new Date(2026,0,1,12,34,56)
 expect(localDateTime(summer.toISOString())).toBe('01.07.2026 12:34:56')
 expect(localDateTime(winter.toISOString())).toBe('01.01.2026 12:34:56')
})
it('preserves original ISO offsets and nanoseconds for inspection and never throws on invalid dates', () => {
 const raw='2026-11-05T21:50:10.123456789+03:00'
 expect(isoDateTime(raw)).toBe(raw)
 expect(isoDateTime(0)).toBe('1970-01-01T00:00:00.000Z')
 expect(localDateTime('not a date')).toBe('not a date')
 expect(localDateTime(NaN)).toBe('—')
})
