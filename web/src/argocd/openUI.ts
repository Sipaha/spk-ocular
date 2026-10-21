import type {Client} from '../api/client'
import type {Ref,Tunnel} from '../api/types'
/** A click may resolve ports and reuse an exact current forward. It never
 * guesses among Services, retries a write, or opens an obsolete browser URL. */
export async function argoUIForward(client:Client,ref:Ref,configRev:string|undefined,current:()=>boolean):Promise<Tunnel|null>{
 const [info,forwards]=await Promise.all([client.forwardInfo(ref),client.listForwards()])
 if(!current())return null
 const ports=info.ports.filter(p=>p.supported&&p.protocol==='TCP'&&(p.scheme==='https'||p.scheme==='http'))
 const port=ports.find(p=>p.port===443&&p.scheme==='https')??(ports.length===1?ports[0]:undefined)
 if(!port)return null
 const existing=configRev?forwards.find(t=>t.target.provider===ref.provider&&t.target.target===ref.target&&t.target.configRev===configRev&&t.target.ref.kind===ref.kind&&t.target.ref.name===ref.name&&(t.target.ref.scope??'')===(ref.scope??'')&&t.target.ref.uid===ref.uid&&!!ref.uid&&t.target.port===port.port&&t.scheme===port.scheme&&['ready','idle'].includes(t.state)&&Number.isInteger(t.localPort)&&t.localPort>0&&t.localPort<=65535):undefined
 if(existing)return existing
 if(!current())return null
 return client.startForward({ref,port:port.port,scheme:port.scheme})
}
