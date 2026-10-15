import { createStore } from 'zustand/vanilla'
import { useStore } from 'zustand'
import type { Client } from '../api/client'
import type { Target } from '../api/types'
import { persistTargetEntries } from '../components/navigationPersist'

// Stable catalogue IDs, independent of translated labels and target names.
export const iconPaths = {
 mountain:'M2 20 9 5l4 8 2-4 7 11H2ZM6 12l3 2 3-3',
 lighthouse:'M8 21h8l-2-11h-4L8 21ZM9 6h6v4H9V6Zm3-4v4M3 5l3 2m12 0 3-2M2 11h4m12 0h4',
 planet:'M17 7a7 7 0 1 0-10 10M7 7a7 7 0 0 1 10 10M2 17c2 3 21-7 20-10S0 14 2 17Z',
 rocket:'M9 15 6 12c3-7 9-9 15-9 0 6-2 12-9 15l-3-3ZM6 12H3l-1 6 6-1m4 1-1 4 6-1v-6M4 21l3-3M15 7h2v2h-2Z',
 tree:'M12 2 5 10h3l-5 7h7v5h4v-5h7l-5-7h3L12 2Z',
 leaf:'M20 3C5 2 0 12 7 18s16-2 13-15ZM5 21 17 7M10 16v-5m4 1h4',
 wave:'M2 16c4 0 5-3 5-7 0-4 5-5 7-2-4-1-4 7 2 7h6M2 21h20',
 flame:'M12 2c2 6 7 7 7 13a7 7 0 0 1-14 0c0-3 2-5 3-7 0 4 2 4 2 4s3-4 2-10ZM12 14c-4 4-2 7 0 7s4-3 0-7Z',
 snowflake:'M12 2v20M3 7l18 10M3 17 21 7M9 4l3 3 3-3M9 20l3-3 3 3M3 10l4-1-1-4m15 9-4 1 1 4M6 19l1-4-4-1m15-9-1 4 4 1',
 crystal:'m8 3 8 0 5 8-9 11-9-11 5-8ZM3 11h18M8 3l-1 8 5 11 5-11-1-8',
 cube:'m12 2 9 5v10l-9 5-9-5V7l9-5Zm-9 5 9 5 9-5M12 12v10',
 layers:'m12 2 10 5-10 5L2 7l10-5ZM2 12l10 5 10-5M2 17l10 5 10-5',
 tower:'M7 3v4h3V3h4v4h3V3h3v8l-3 3v7H7v-7l-3-3V3h3ZM10 21v-5h4v5',
 castle:'M3 21V8h5v5h8V8h5v13H3ZM3 8V4m5 4V4m8 4V4m5 4V4M10 21v-5h4v5',
 bridge:'M2 20h20M5 20V5m14 15V5M5 7c3 8 11 8 14 0M2 15h20M9 15v-3m6 3v-3',
 anchor:'M12 7v15M8 4a4 4 0 1 0 8 0 4 4 0 0 0-8 0ZM2 13c0 12 20 12 20 0M2 13l-1 4m1-4 4 1m16-1 1 4m-1-4-4 1M7 10h10',
 compass:'M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20Zm5 5-3 7-7 3 3-7 7-3Z',
 target:'M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20Zm0 4a6 6 0 1 0 0 12 6 6 0 0 0 0-12Zm0 4a2 2 0 1 0 0 4 2 2 0 0 0 0-4Z',
 star:'m12 2 3 7 7 1-5 5 1 7-6-3-6 3 1-7-5-5 7-1 3-7Z',
 moon:'M20 15A9 9 0 0 1 9 4a9 9 0 1 0 11 11Z',
 sun:'M12 7a5 5 0 1 0 0 10 5 5 0 0 0 0-10ZM12 1v3m0 16v3M1 12h3m16 0h3M4 4l2 2m12 12 2 2M4 20l2-2M18 6l2-2',
 cloud:'M6 19a5 5 0 0 1-1-10 7 7 0 0 1 14-1 6 6 0 0 1 0 11H6Z',
 bolt:'m13 2-9 12h7l-1 8 10-13h-8l1-7Z',
 atom:'M12 10a2 2 0 1 0 0 4 2 2 0 0 0 0-4ZM2 12c0-5 20-5 20 0S2 17 2 12ZM7 3c4-2 14 15 10 18S3 5 7 3Zm10 0C13 1 3 18 7 21S21 5 17 3Z',
 flask:'M9 2h6M10 2v7L3 20q-1 2 2 2h14q3 0 2-2L14 9V2M7 14h10',
 gear:'m9 3 1-2h4l1 2 3 2 2 0 2 4-1 2v3l1 2-2 4h-3l-2 2h-4l-1-2-3-2H3l-2-4 1-2v-3L1 7l2-4h3l3 0ZM8 12a4 4 0 1 0 8 0 4 4 0 0 0-8 0Z',
 key:'M14 4a6 6 0 1 0 0 12 6 6 0 0 0 0-12ZM9 14l-8 8m2-2 2 2m1-5 2 2M16 8h.01',
 shield:'m12 2 9 4v6c0 6-9 10-9 10S3 18 3 12V6l9-4ZM8 12l3 3 5-6',
 feather:'M3 21 17 7M8 16C-1 12 10 1 19 2c4 8-1 17-11 14ZM9 15h6M13 11h7',
 fish:'M6 12C10 3 17 5 22 12c-5 7-12 9-16 0Zm0 0L2 6v12l4-6ZM17 10h.01M12 6l-2-3m2 15-2 3',
 bird:'M3 17c5 1 7-1 8-4-6 0-7-4-7-8 4 1 7 3 9 5 0-6 6-7 8-2l2 1-3 2c-1 9-9 12-17 6Z',
 cat:'M4 11V3l6 4h4l6-4v8a8 8 0 0 1-16 0ZM8 12h.01M16 12h.01M10 16l2 1 2-1M2 13h4m12 0h4M2 17l4-1m12 0 4 1',
} as const
export type IconId = keyof typeof iconPaths
export const iconIds = Object.keys(iconPaths) as IconId[]
export const palette = {
 blue:['#1c4f8a','#e1edfc','#a6ceff','#20354c'],
 teal:['#00645d','#d9f1ec','#8dd9cc','#193d39'],
 green:['#38601c','#e6f0d9','#bbd99b','#303d23'],
 amber:['#805000','#fff0cc','#f3ce85','#49371d'],
 coral:['#983c2a','#fce6df','#ffb6a5','#4a2b26'],
 rose:['#8c355b','#f9e2ed','#efa8ca','#472b3b'],
 violet:['#63409a','#eee5fb','#cfb3f5','#362a49'],
 slate:['#405167','#e5eaf0','#becddd','#2e3845'],
} as const
export type PaletteId = keyof typeof palette
export const paletteIds = Object.keys(palette) as PaletteId[]
export interface Appearance { icon: IconId; palette: PaletteId }
export function parseAppearance(raw: string | undefined): Appearance | null {
 try { const a: unknown = JSON.parse(raw ?? ''); if (typeof a !== 'object' || !a) return null
  const {icon,palette:color}=a as Record<string,unknown>
  return typeof icon==='string' && Object.hasOwn(iconPaths,icon) && typeof color==='string' && Object.hasOwn(palette,color) ? {icon:icon as IconId,palette:color as PaletteId} : null
 } catch { return null }
}
const keyOf = (t: Pick<Target,'provider'|'id'>) => JSON.stringify([t.provider,t.id])
interface State { entries: Record<string,Appearance>; errors: Record<string,boolean> }
function makeModel(client: Client) {
 const store=createStore<State>(()=>({entries:{},errors:{}}))
 const initialized=new Set<string>(),pending=new Map<string,Promise<void>>()
 const put=(key:string,value:Appearance)=>store.setState(s=>({entries:{...s.entries,[key]:value}}))
 const error=(key:string,value:boolean)=>store.setState(s=>({errors:{...s.errors,[key]:value}}))
 const persist=async(target:Target,value:Appearance)=>{await persistTargetEntries(client,target.provider,target.id,{appearance:JSON.stringify(value)})}
 const ensure=(target:Target,retry=false):Promise<void>=>{
  const key=keyOf(target)
  if(initialized.has(key)||(!retry&&store.getState().errors[key]))return Promise.resolve()
  if(pending.has(key))return pending.get(key)!
  const job=(async()=>{
   try {
    let value=store.getState().entries[key]
    if(!value){
     const saved=parseAppearance((await client.getTargetState(target.provider,target.id)).appearance)
     if(saved){put(key,saved);initialized.add(key);error(key,false);return}
     const used=new Set(Object.values(store.getState().entries).map(a=>`${a.icon}/${a.palette}`))
     const choices=iconIds.flatMap(icon=>paletteIds.map(palette=>({icon,palette}))).filter(a=>!used.has(`${a.icon}/${a.palette}`))
     const available=choices.length?choices:iconIds.flatMap(icon=>paletteIds.map(palette=>({icon,palette})))
     value=available[crypto.getRandomValues(new Uint32Array(1))[0]%available.length];put(key,value)
    }
    await persist(target,value);initialized.add(key);error(key,false)
   } catch {error(key,true)}
  })().finally(()=>pending.delete(key))
  pending.set(key,job);return job
 }
 return {store,ensure,async save(target:Target,value:Appearance){
  await ensure(target,true)
  const key=keyOf(target)
  if(!initialized.has(key))throw new Error('appearance not initialized')
  // Commit only after persistence. An older initialization cannot replace it.
  await persist(target,value);put(key,value);error(key,false)
 }}
}
const models=new WeakMap<Client,ReturnType<typeof makeModel>>()
export function appearanceModel(client:Client){let model=models.get(client);if(!model){model=makeModel(client);models.set(client,model)}return model}
export function useAppearance(client:Client,target:Target){return useStore(appearanceModel(client).store,s=>s.entries[keyOf(target)])}
