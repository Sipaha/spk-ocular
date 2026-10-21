import {it,expect,vi} from 'vitest'
import {fakeClient} from '../test/fakeClient'
import {argoUIForward} from './openUI'
import type {Tunnel} from '../api/types'
const ref={provider:'synthetic',target:'demo',kind:'services',scope:'blue',name:'argocd-server',uid:'service-current'}
function fixture(){
 const {client}=fakeClient([])
 vi.mocked(client.forwardInfo).mockResolvedValue({ports:[{port:80,protocol:'TCP',scheme:'http',supported:true},{port:443,protocol:'TCP',scheme:'https',supported:true}]})
 const tunnel={id:'existing',target:{provider:'synthetic',target:'demo',targetTitle:'demo',ref,configRev:'cfg',port:443},localPort:54433,scheme:'https',state:'ready'} as Tunnel
 vi.mocked(client.startForward).mockResolvedValue(tunnel)
 return {client,tunnel}
}
it('reuses an exact current UID/configuration HTTPS forward without a second write',async()=>{
 const {client,tunnel}=fixture();vi.mocked(client.listForwards).mockResolvedValue([tunnel])
 expect(await argoUIForward(client,ref,'cfg',()=>true)).toBe(tunnel)
 expect(client.startForward).not.toHaveBeenCalled()
})
it.each(['configuration','UID','failed'])('rejects an obsolete %s forward and starts exactly one current forward',async reason=>{
 const {client,tunnel}=fixture();const old={...tunnel,target:{...tunnel.target,configRev:reason==='configuration'?'old':'cfg',ref:{...ref,uid:reason==='UID'?'old':ref.uid}},state:reason==='failed'?'error' as const:'ready' as const};vi.mocked(client.listForwards).mockResolvedValue([old])
 await argoUIForward(client,ref,'cfg',()=>true)
 expect(client.startForward).toHaveBeenCalledExactlyOnceWith({ref,port:443,scheme:'https'})
})
it('a late port answer cannot start a forward for an abandoned context',async()=>{
 const {client}=fixture();let current=true;vi.mocked(client.forwardInfo).mockImplementation(async()=>{current=false;return {ports:[{port:443,protocol:'TCP',scheme:'https',supported:true}]}})
 expect(await argoUIForward(client,ref,'cfg',()=>current)).toBeNull();expect(client.startForward).not.toHaveBeenCalled()
})
it('ambiguous nonstandard ports fall back to explicit port controls; errors are not retried',async()=>{
 const {client}=fixture();vi.mocked(client.forwardInfo).mockResolvedValue({ports:[{port:8443,protocol:'TCP',scheme:'https',supported:true},{port:8080,protocol:'TCP',scheme:'http',supported:true}]})
 expect(await argoUIForward(client,ref,'cfg',()=>true)).toBeNull();expect(client.startForward).not.toHaveBeenCalled()
 vi.mocked(client.forwardInfo).mockResolvedValue({ports:[{port:443,protocol:'TCP',scheme:'https',supported:true}]});vi.mocked(client.startForward).mockRejectedValue(new Error('unknown outcome'))
 await expect(argoUIForward(client,ref,'cfg',()=>true)).rejects.toThrow('unknown outcome');expect(client.startForward).toHaveBeenCalledTimes(1)
})
