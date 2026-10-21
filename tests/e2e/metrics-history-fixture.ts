import {createServer} from 'node:http'
import {reliabilityServer} from './reliability-fixture'
export const historyUID='12345678-abcd-1234-abcd-123456789012'
export async function metricsHistoryFixture(){
 const kube=await reliabilityServer(),requests:{query:string;start:string;end:string;step:string;method:string;authorized:boolean}[]=[]
 kube.data.pods=[{apiVersion:'v1',kind:'Pod',metadata:{name:'web',namespace:'blue',uid:historyUID,resourceVersion:'1',creationTimestamp:new Date(Date.now()-3*86400000).toISOString()},spec:{containers:[{name:'app',image:'example.invalid/app:test',env:[{name:'PRIVATE',value:'PRIVATE-ENV'}],resources:{requests:{cpu:'100m',memory:'64Mi'},limits:{cpu:'1',memory:'128Mi'}}}]},status:{phase:'Running'}}]
 let mode='available',cancelled=0
 const source=createServer((req,res)=>{
  const u=new URL(req.url??'/','http://127.0.0.1'),q=u.searchParams
  requests.push({query:q.get('query')??'',start:q.get('start')??'',end:q.get('end')??'',step:q.get('step')??'',method:req.method??'',authorized:req.headers.authorization==='Bearer fixture-history-token'})
  res.setHeader('Content-Type','application/json')
  if(mode==='hold'){res.on('close',()=>{cancelled++});return}
  if(req.method!=='GET'||u.pathname!=='/api/v1/query_range'){res.writeHead(405);res.end('{}');return}
  const query=q.get('query')??'',metric=query.includes('working_set')?'memory':query.includes('throttled')?'throttling':'cpu'
  if(mode==='denied'&&metric==='throttling'){res.writeHead(403);res.end(JSON.stringify({error:'PRIVATE-UPSTREAM'}));return}
  const start=Number(q.get('start')),end=Number(q.get('end')),values:(number|string)[][]=[]
  for(let at=start;at<=end;at+=60){if((at-start)/60%9===4)continue;values.push([at,at===start?'0':String(metric==='memory'?64*1024*1024+(Math.sin(at/300)+1)*10*1024*1024:metric==='throttling'?.12:(Math.sin(at/400)+1)*.2)])}
  res.end(JSON.stringify({status:'success',data:{resultType:'matrix',result:[{metric:{namespace:'blue',pod:'web',container:'app',id:`/kubepods/burstable/pod${historyUID}/container-runtime`,cluster:mode==='foreign'?'other':'alpha'},values}]}}))
 })
 await new Promise<void>(r=>source.listen(0,'127.0.0.1',r))
 return {kube,sourceURL:`http://127.0.0.1:${(source.address() as {port:number}).port}`,requests,setMode:(value:string)=>{mode=value},cancelled:()=>cancelled,close:async()=>{source.closeAllConnections();await new Promise<void>(r=>source.close(()=>r()));await kube.close()}}
}
