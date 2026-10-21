import type {Client} from '../api/client'
import type {ScopeSel} from '../api/types'
import type {FluxSnapshot} from './types'
const snapshots=new WeakMap<Client,Map<string,FluxSnapshot>>()
const listeners=new Set<()=>void>()
export const evidenceKey=(provider:string,target:string,connection:string|number,scope:ScopeSel)=>JSON.stringify([provider,target,connection,scope])
export const subscribeEvidence=(changed:()=>void)=>{listeners.add(changed);return()=>{listeners.delete(changed)}}
export const readEvidence=(client:Client,key:string)=>snapshots.get(client)?.get(key)??null
/** Only ordinary controller projections are shared, in memory, for the exact
 * connection and selection. Only finite controller projections enter this bounded cache. */
export function shareEvidence(client:Client,key:string,snapshot:FluxSnapshot){
 let cache=snapshots.get(client)
 if(!cache){cache=new Map();snapshots.set(client,cache)}
 if(cache.get(key)===snapshot)return
 cache.delete(key);cache.set(key,snapshot)
 while(cache.size>8)cache.delete(cache.keys().next().value!)
 listeners.forEach(changed=>changed())
}
