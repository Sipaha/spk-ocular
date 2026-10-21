import {useEffect,useRef,useState} from 'react'
import type {Client} from '../api/client'
import type {ViewPresetRegistry} from '../api/types'
import {emptyRegistry} from './model'
const initial=(key:string)=>({key,load:0,registry:emptyRegistry(),ready:false,busy:false,error:''})
/** Revision fences other windows; key/load tokens fence late reads and saves. */
export function usePresets(client: Client, provider: string, kind: string, enabled: boolean) {
  const key=JSON.stringify([provider,kind])
  const [state,setState]=useState(()=>initial(key))
  const saving=useRef(false)
  if(state.key!==key)setState(initial(key))
  const load=state.load
  useEffect(()=>{
    if(!enabled)return
    let live=true
    void client.viewPresets({provider,kind}).then(registry=>{
      if(live)setState(s=>s.key===key&&s.load===load?{...s,registry,ready:true,error:''}:s)
    },()=>{if(live)setState(s=>s.key===key&&s.load===load?{...s,error:'load'}:s)})
    return ()=>{live=false}
  },[client,provider,kind,key,enabled,load])
  async function save(registry:ViewPresetRegistry){
    if(!state.ready||saving.current)return false
    saving.current=true
    setState(s=>({...s,busy:true}))
    try{
      const result=await client.viewPresets({provider,kind,registry})
      setState(s=>s.key===key&&s.load===load?{...s,registry:result,error:'',busy:false}:s)
      return true
    }catch{
      setState(s=>s.key===key&&s.load===load?{...s,error:'save',busy:false}:s)
      return false
    }finally{saving.current=false}
  }
  return {...state,save,reload:()=>setState(s=>({...s,load:s.load+1,ready:false,error:''}))}
}
