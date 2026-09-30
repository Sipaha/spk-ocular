import { useMemo } from 'react'
import { t } from '../i18n'
import { Select } from './Select'

interface Props {
  /** The chosen name; '' is all. */
  value: string
  names: string[]
  /** The picker's name ("Namespace"). */
  label: string
  /** The '' choice ("All namespaces"). */
  allLabel: string
  onChange: (name: string) => void
}

/** The scope picker: the app's select with search, "all" on top while not searching. */
export function ScopeSelect({ value, names, label, allLabel, onChange }: Props) {
  const options = useMemo(() => [{ value: '', label: allLabel, pinned: true }, ...names.map((n) => ({ value: n, label: n }))], [names, allLabel])
  return <Select value={value} options={options} label={label} onChange={onChange} search searchLabel={t('scope.search', { scope: label.toLowerCase() })} className="max-w-64" />
}
