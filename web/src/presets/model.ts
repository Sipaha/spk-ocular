import type {Column, Row, ViewPreset, ViewPresetRegistry} from '../api/types'
export const labelID = (key: string) => `metadata.labels:${key}`
const bytes = (s: string) => new TextEncoder().encode(s).length
export function validLabelKeys(keys: string[]): boolean {
  return keys.length <= 8 && new Set(keys).size === keys.length && keys.every(k => bytes(k) > 0 && bytes(k) <= 253 && !/[\s\p{Cc}]/u.test(k))
}
export function labelKeys(text: string): string[] { return text === '' ? [] : text.split('\n') }
export const emptyRegistry = (): ViewPresetRegistry => ({version:1,revision:0,active:'',presets:[]})
export function columnsFor(base: Column[], preset?: ViewPreset): Column[] {
  if (!preset) return base
  const all = [...base,...preset.labels.map(key=>({id:labelID(key),title:key,type:'text' as const,width:180}))]
  const rank = new Map(preset.order.map((id,i)=>[id,i]))
  return all.map((c,i)=>({...c,hidden:c.id !== 'name' && preset.hidden.includes(c.id),rank:rank.get(c.id) ?? preset.order.length+i})).sort((a,b)=>a.rank-b.rank)
}
export function rowsFor(rows: Row[], base: Column[], columns: Column[], preset?: ViewPreset): Row[] {
  if (!preset) return rows
  const indices = new Map(base.map((c,i)=>[c.id,i]))
  const labels = new Map(preset.labels.map(k=>[labelID(k),k]))
  return rows.map(row=>({...row,cells:columns.map(c=>{
    const key = labels.get(c.id)
    if (key !== undefined) { const value = row.labels?.[key]; return value?.state === 'value' ? {text:value.value ?? ''} : {} }
    return row.cells[indices.get(c.id) ?? -1] ?? {}
  })}))
}
export function labelState(row: Row, key: string) { return row.labels && Object.hasOwn(row.labels,key) ? row.labels[key]?.state ?? 'unavailable' : 'unavailable' }
