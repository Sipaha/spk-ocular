import { useMemo } from 'react'
import { t } from '../i18n'
import type { ScopeSel } from '../api/types'
import { scopeSet, selectedScopes } from '../scopes'
import { editsHeld } from '../edit/guard'
import { Select, type SelectMemory } from './Select'

interface Props {
  value: ScopeSel
  memory: SelectMemory
  names: string[]
  /** The picker's name ("Namespace"). */
  label: string
  /** The '' choice ("All namespaces"). */
  allLabel: string
  onChange: (scope: ScopeSel) => void
}

/** The scope picker: the app's select with search, "all" on top while not searching. */
export function ScopeSelect({ value, names, label, allLabel, onChange, memory }: Props) {
  const options = useMemo(() => [{ value: '', label: allLabel, pinned: true }, ...names.map((n) => ({ value: n, label: n }))], [names, allLabel])
  const selected = selectedScopes(value)
  const summary = value.mode === 'all' ? allLabel : selected.length ? selected.join(', ') : t('scope.empty')
  return <Select value={value.mode === 'all' ? '' : value.mode === 'one' ? value.name! : '\u0000'} options={options} label={label}
    onChange={(name) => onChange(name ? { mode: 'one', name } : { mode: 'all' })}
    multiple={{ selected, summary, closeOnToggle: editsHeld, onToggle: (name) => onChange(scopeSet(selected.includes(name) ? selected.filter((n) => n !== name) : [...selected, name])) }}
    memory={memory} search searchLabel={t('scope.search', { scope: label.toLowerCase() })} className="max-w-64" />
}
