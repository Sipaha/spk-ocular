import {create} from 'zustand'
import type {Client} from '../api/client'
import type {LogComposition, Ref, Target, TargetsView} from '../api/types'

const supported=new Set(['pods','apps/deployments','apps/replicasets','apps/statefulsets','apps/daemonsets'])
export const compositionRoot=(r:Ref)=>r.provider==='kubernetes'&&supported.has(r.kind)&&!!r.uid&&!!r.scope
export const compositionKey=(c:LogComposition)=>JSON.stringify([c.configRev,c.connectionId,c.refs.map(r=>[r.provider,r.target,r.scope,r.kind,r.name,r.uid]).sort((a,b)=>JSON.stringify(a).localeCompare(JSON.stringify(b)))])
export const copyComposition=(c:LogComposition):LogComposition=>({...c,refs:c.refs.map(r=>({...r}))})
export function compositionAvailable(c:LogComposition,view:TargetsView|null){
 const ref=c.refs[0],target=view?.groups.flatMap(g=>g.targets).find(t=>t.provider===ref?.provider&&t.id===ref.target)
 return target?.connection?.state==='connected'&&target.connection.id===c.connectionId&&target.configRev===c.configRev
}
interface Tray {composition:LogComposition|null;targetTitle:string;review:boolean}
export const useLogComposition=create<Tray>(()=>({composition:null,targetTitle:'',review:false}))
export const clearLogComposition=()=>useLogComposition.setState({composition:null,targetTitle:'',review:false})
export function addLogRoots(refs:Ref[],target:Target):boolean{
 if(!target.configRev||target.connection?.state!=='connected'||!refs.length||refs.some(r=>!compositionRoot({...r,provider:target.provider})||!!r.provider&&r.provider!==target.provider||!!r.target&&r.target!==target.id))return false
 const old=useLogComposition.getState().composition
 if(old&&(old.configRev!==target.configRev||old.connectionId!==target.connection.id||old.refs[0].provider!==target.provider||old.refs[0].target!==target.id))return false
 const next=old?copyComposition(old):{refs:[],configRev:target.configRev,connectionId:target.connection.id}
 for(const ref of refs){const r={...ref,provider:target.provider,target:target.id};if(!next.refs.some(x=>x.scope===r.scope&&x.uid===r.uid))next.refs.push(r)}
 if(next.refs.length>8)return false
 useLogComposition.setState({composition:next,targetTitle:target.title});return true
}
export function removeLogRoot(index:number){useLogComposition.setState(s=>({composition:s.composition?{...s.composition,refs:s.composition.refs.filter((_,i)=>i!==index)}:null}))}

// The existing viewer remains the sole buffer/stream owner. Only these reads
// are redirected; native destination and one-shot transport methods stay intact.
export function composedLogClient(client:Client,composition:LogComposition):Client{
 return {...client,
  logInfo:async()=>({channels:[],defaultChannel:'*',aggregate:true,previous:false,channelLabel:{key:'kubernetes.level.container',text:'Container'},allChannelsLabel:{key:'kubernetes.logs.allContainers',text:'All containers'}}),
  openLogStream:(_ref,query)=>{const wire={...query};delete wire.compositionKey;return client.openComposedLogStream(composition,wire)},
  openLogHistory:(_ref,_channel,_previous,signal)=>client.openComposedLogHistory(composition,signal),
 }
}
