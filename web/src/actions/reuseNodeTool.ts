import type {Client} from '../api/client'
import type {ActionResult,Ref} from '../api/types'

/** A warm admission is usable only as returned; failure never falls back to CREATE. */
export async function reuseNodeTool(client:Client,ref:Ref,action:string,current:()=>boolean,adopt:(result:ActionResult)=>void):Promise<boolean>{
 if(ref.provider!=='kubernetes'||ref.kind!=='nodes'||!['node-shell','node-files'].includes(action))return false
 const plan=await client.prepareAction(ref,action,{})
 if(!current())return true
 if(!plan.reuse||plan.unavailable||plan.rights.state==='denied'||!plan.expect)return false
 const result=await client.runAction(plan)
 if(current()){adopt(result);return true}
 // A late response belongs to the abandoned request, never the current selection.
 await Promise.allSettled([
  ...(result.terminal?.preparedId?[client.forgetTerminal(result.terminal.preparedId)]:[]),
  ...(result.files?[client.files({ref:result.files.ref,preparedId:result.files.preparedId,instance:'',channel:'',path:'/',command:'close'})]:[]),
 ])
 return true
}
