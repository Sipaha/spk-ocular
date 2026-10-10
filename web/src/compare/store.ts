import {create} from 'zustand'
import type {CompareSelection, Ref, Target, TargetsView} from '../api/types'

interface ComparisonState {baseline:CompareSelection|null;other:CompareSelection|null}
// Only references are held between resource/target navigation. YAML is owned by
// the open dialog and is discarded on close, never persisted in settings.
export const useComparison=create<ComparisonState>(()=>({baseline:null,other:null}))
export const clearComparison=()=>useComparison.setState({baseline:null,other:null})
export const pinComparison=(selection:CompareSelection)=>useComparison.setState({baseline:selection,other:null})
export const compareWith=(selection:CompareSelection)=>useComparison.setState({other:selection})
export const closeComparison=()=>useComparison.setState({other:null})
export function sameResource(a:Ref,b:Ref){return a.provider===b.provider&&a.target===b.target&&a.kind===b.kind&&a.scope===b.scope&&a.name===b.name&&a.uid===b.uid}
export function comparisonSelection(ref:Ref,target:Target|undefined):CompareSelection|null{
 if(!ref.uid||!target?.configRev||target.connection?.state!=='connected')return null
 return {ref:{...ref,provider:target.provider,target:target.id},configRev:target.configRev,connectionId:target.connection.id,targetTitle:target.title}
}
export function selectionAvailable(selection:CompareSelection,view:TargetsView|null){
 const target=view?.groups.flatMap(g=>g.targets).find(t=>t.provider===selection.ref.provider&&t.id===selection.ref.target)
 return target?.connection?.state==='connected'&&target.connection.id===selection.connectionId&&target.configRev===selection.configRev
}
