import {useEffect} from 'react'
import {create} from 'zustand'
import type {Client} from '../api/client'
import {usePanelWidths} from '../components/PanelResize'
import {persistTargetEntries} from '../components/navigationPersist'
import {dock,useDock} from '../dock/store'
import {useStore} from '../store'
import {WORKSPACE_KEY,WORKSPACE_PROVIDER,WORKSPACE_TARGET,parseWorkspace,serializeWorkspace} from './snapshot'

export const useWorkspaceStatus=create<{ready:boolean;error:'load'|'save'|'invalid'|'limit'|null;retry:()=>void;reset:()=>void}>(()=>({ready:false,error:null,retry:()=>{},reset:()=>{}}))

/** Serialized metadata writes share the existing target queue across remounts.
 * No delayed write is left waiting on window close. Failed/invalid reads never
 * enable writes, and an older reply cannot replace a newer failure/success. */
export function useWorkspacePersistence(client:Client){
 useEffect(()=>{
  let live=true,lastNoted:string|null=null,sequence=0,loadSequence=0
  let offDock=()=>{},offWidths=()=>{}
  const state=useWorkspaceStatus
  state.setState({ready:false,error:null})
  const note=()=>{
   const text=serializeWorkspace(useDock.getState(),usePanelWidths.getState())
   if(text===null){state.setState({error:'limit'});return}
   if(text===lastNoted)return
   lastNoted=text
   const seq=++sequence
   void persistTargetEntries(client,WORKSPACE_PROVIDER,WORKSPACE_TARGET,{[WORKSPACE_KEY]:text}).then(()=>{if(live&&seq===sequence)state.setState({error:null})},()=>{if(live&&seq===sequence){lastNoted=null;state.setState({error:'save'})}})
  }
  const subscribe=()=>{offDock();offWidths();offDock=useDock.subscribe(note);offWidths=usePanelWidths.subscribe(note)}
  const load=async()=>{
   const own=++loadSequence
   state.setState({ready:false,error:null})
   const initialWidths=usePanelWidths.getState(),initialHeight=useDock.getState().height
   let text:string|undefined
   try{text=(await client.getTargetState(WORKSPACE_PROVIDER,WORKSPACE_TARGET))[WORKSPACE_KEY]}catch{if(live&&own===loadSequence)state.setState({error:'load'});return}
   if(!live||own!==loadSequence)return
   let snapshot
   try{snapshot=parseWorkspace(text)}catch{state.setState({error:'invalid'});return}
   if(snapshot){
    dock.restore(snapshot)
    const current=usePanelWidths.getState()
    usePanelWidths.setState(Object.fromEntries(Object.entries(snapshot.widths).filter(([key])=>current[key as keyof typeof current]===initialWidths[key as keyof typeof current])))
    if(useDock.getState().height===initialHeight)dock.setHeight(Math.min(snapshot.height,Math.max(140,window.innerHeight*0.85)))
   }
   if(!text&&useDock.getState().height===initialHeight){
    // Migrate the selected target's existing bottom-panel size once; the new
    // workspace layout is global and thereafter takes precedence.
    try{
     const selected=(await client.listTargets()).selected
     if(selected){
      const old=await client.getTargetState(selected.provider,selected.id)
      const height=Number(old.logsHeight),current=useStore.getState().view?.selected
      if(live&&own===loadSequence&&useDock.getState().height===initialHeight&&Number.isFinite(height)&&height>=140&&height<=1200&&(!current||current.provider===selected.provider&&current.id===selected.id))dock.setHeight(Math.min(height,Math.max(140,window.innerHeight*0.85)))
     }
    }catch{/* Legacy optional preferences must not block valid workspace metadata. */}
    if(!live||own!==loadSequence)return
   }
   const changedBeforeLoad=initialWidths!==usePanelWidths.getState()||initialHeight!==useDock.getState().height
   lastNoted=text??(changedBeforeLoad?null:serializeWorkspace(useDock.getState(),usePanelWidths.getState()))
   state.setState({ready:true,error:null});subscribe();note()
  }
  state.setState({retry:()=>{if(state.getState().ready){lastNoted=null;note()}else void load()},reset:()=>{loadSequence++;state.setState({ready:true,error:null});subscribe();lastNoted=null;note()}})
  void load()
  return()=>{live=false;offDock();offWidths()}
 },[client])
}
