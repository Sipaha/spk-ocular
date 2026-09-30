import { describe, expect, it } from 'vitest'
import type { AgentAuditEntry } from '../api/types'
import { outcomeText } from './AgentsPanel'

const e = (phase: AgentAuditEntry['phase'], outcome?: string): AgentAuditEntry => ({ id: 1, at: '', agent: 'a', method: 'RunAction', phase, outcome, count: 1 })

describe('outcomeText', () => {
  it('says a record in words; an unknown outcome as it is', () => {
    expect(outcomeText(e('read', 'done'))).toBe('read')
    expect(outcomeText(e('intent'))).toBe('about to write')
    expect(outcomeText(e('refused', 'forbidden'))).toBe('refused · access denied')
    expect(outcomeText(e('outcome', 'rejected'))).toBe('you said no')
    expect(outcomeText(e('outcome', 'awaiting_confirmation'))).toBe('waits for your confirmation')
    expect(outcomeText(e('outcome', '2 of 3 done'))).toBe('2 of 3 done')
    expect(outcomeText(e('outcome'))).toBe('outcome')
  })
})
