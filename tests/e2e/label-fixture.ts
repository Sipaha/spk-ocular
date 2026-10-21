import {createServer} from 'node:http'
/** Read-only loopback Kubernetes wire fixture; every request is attributable. */
export async function labelServer(count=2000){
 const requests:{method:string;path:string;watch:boolean;query:Record<string,string>;at:number}[]=[]
 const pods=Array.from({length:count},(_,i)=>({apiVersion:'v1',kind:'Pod',metadata:{name:`pod-${String(i).padStart(4,'0')}`,namespace:'blue',uid:`uid-${i}`,resourceVersion:'1',creationTimestamp:'2026-10-21T00:00:00Z',labels:{'app.kubernetes.io/version':i===count-1?'needle':'v1','team/name':'commerce','x:y/z.a':`literal-${i}`,...(i===0?{empty:'',large:'é'.repeat(129)}:{})}},spec:{containers:[{name:'app',image:'example.invalid/fixture:v1',env:[{name:'PRIVATE',value:'NEVER-PROJECT-THIS'}]}]},status:{phase:'Running',conditions:[{type:'Ready',status:'True'}]}}))
 const server=createServer((req,res)=>{
  const u=new URL(req.url??'/', 'http://127.0.0.1'),path=u.pathname
  requests.push({method:req.method??'',path,watch:u.searchParams.get('watch')==='true',query:Object.fromEntries(u.searchParams),at:Date.now()})
  const send=(data:unknown,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(data))}
  const fail=(status:number)=>send({apiVersion:'v1',kind:'Status',status:'Failure',code:status,reason:status===404?'NotFound':'MethodNotAllowed',message:'Owned read-only fixture'},status)
  if(req.method!=='GET')return fail(405)
  if(path==='/version')return send({gitVersion:'v1.34.0',major:'1',minor:'34'})
  if(path==='/api')return send({apiVersion:'v1',kind:'APIVersions',versions:['v1']})
  if(path==='/apis')return send({apiVersion:'v1',kind:'APIGroupList',groups:[]})
  if(path==='/api/v1')return send({apiVersion:'v1',kind:'APIResourceList',groupVersion:'v1',resources:[{name:'pods',singularName:'pod',kind:'Pod',namespaced:true,verbs:['get','list','watch','patch']},{name:'namespaces',singularName:'namespace',kind:'Namespace',namespaced:false,verbs:['get','list','watch']}]})
  const match=path.match(/^\/api\/v1\/(?:namespaces\/([^/]+)\/)?(pods|namespaces)(?:\/([^/]+))?$/)
  if(!match)return fail(404)
  const [,ns,kind,name]=match
  let rows:Record<string,unknown>[]=kind==='pods'?(!ns||ns==='blue'?pods:[]):[{apiVersion:'v1',kind:'Namespace',metadata:{name:'blue',uid:'ns-blue',resourceVersion:'1'}}]
  const field=u.searchParams.get('fieldSelector');if(field?.startsWith('metadata.name='))rows=rows.filter(r=>(r.metadata as {name:string}).name===field.slice(14))
  if(name){const row=rows.find(r=>(r.metadata as {name:string}).name===name);return row?send(row):fail(404)}
  if(u.searchParams.get('watch')==='true'){
   res.writeHead(200,{'Content-Type':'application/json'})
   if(u.searchParams.get('sendInitialEvents')==='true'){
    for(const row of rows)res.write(JSON.stringify({type:'ADDED',object:row})+'\n')
    res.write(JSON.stringify({type:'BOOKMARK',object:{apiVersion:'v1',kind:kind==='pods'?'Pod':'Namespace',metadata:{resourceVersion:'1',annotations:{'k8s.io/initial-events-end':'true'}}}})+'\n')
   }else res.write('\n')
   return
  }
  const limit=Number(u.searchParams.get('limit')??0),offset=Number(u.searchParams.get('continue')??0),page=limit>0?rows.slice(offset,offset+limit):rows
  return send({apiVersion:'v1',kind:kind==='pods'?'PodList':'NamespaceList',metadata:{resourceVersion:'1',...(limit>0&&offset+limit<rows.length?{continue:String(offset+limit)}:{})},items:page})
 })
 await new Promise<void>(resolve=>server.listen(0,'127.0.0.1',resolve))
 return {port:(server.address() as {port:number}).port,requests,close:async()=>{server.closeAllConnections();await new Promise<void>(resolve=>server.close(()=>resolve()))}}
}
