import { describe, expect, it } from 'vitest'
import type { AgentTarget } from '../api/types'
import { scopesFromTarget, settingsOf } from './policy'

describe('group policy migration', () => {
  it('preserves repeated verbs and their individual kind and confirmation boundaries', () => {
    const scope = { mode: 'one' as const, name: 'web' }
    const saved: AgentTarget = { provider: 'k', target: 't', title: 'T', identity: 'i', grants: [
      { scope, verb: 'read', kinds: null },
      { scope, verb: 'read', kinds: ['pods'] },
      { scope, verb: 'action:delete', kinds: ['pods'], noConfirm: true },
      { scope, verb: 'action:delete', kinds: ['services'] },
    ] }
    const migrated = settingsOf(scopesFromTarget(saved))
    expect(migrated.groups).toHaveLength(2)
    expect(migrated.groups!.flatMap((g) => g.grants)).toEqual([
      saved.grants[0], saved.grants[2], saved.grants[1], saved.grants[3],
    ])
    const reloaded = scopesFromTarget({ ...saved, grants: [], ...migrated })
    expect(settingsOf(reloaded)).toEqual(migrated)
  })

  it('keeps empty named groups and master switches without manufacturing grants', () => {
    const scope = { mode: 'one' as const, name: 'web' }
    const saved: AgentTarget = { provider: 'k', target: 't', title: 'T', identity: 'i', grants: [],
      groups: [{ id: 'g', name: 'Maintenance', scope, disabled: true, grants: [] }], disabledScopes: [scope] }
    expect(settingsOf(scopesFromTarget(saved))).toEqual({ groups: saved.groups, disabledScopes: saved.disabledScopes })
  })
})
