import { afterEach, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { DateText } from './DateText'
import { localDateTime } from '../dates'
import { setLanguage } from '../i18n'
import { languages } from '../languages'
afterEach(()=>setLanguage('en'))
it.each(languages)('shows local day-first time with original ISO hover in %s', language => {
 setLanguage(language)
 const raw='2026-11-05T21:50:10Z'
 const {unmount}=render(<DateText value={raw}/>)
 const time=screen.getByText(localDateTime(raw))
 expect(time.tagName).toBe('TIME');expect(time).toHaveAttribute('title',raw);expect(time).toHaveAttribute('datetime',raw)
 unmount()
})
it('retains suffixes in Docker health facts and plain resource identifiers', () => {
 const raw='2026-11-05T21:50:10Z'
 const {container,rerender}=render(<DateText value={raw+', exit 0: healthy'}/>)
 expect(container.textContent).toBe(localDateTime(raw)+', exit 0: healthy')
 rerender(<DateText value="registry.example/image:2026-11-05"/>);expect(container.textContent).toBe('registry.example/image:2026-11-05')
 rerender(<DateText value="2026-99-99T21:50:10Z"/>);expect(container.querySelector('time')).toBeNull()
})
