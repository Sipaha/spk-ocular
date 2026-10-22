import {createServer} from 'node:http'
export async function capacityServer(){
 const requests:{method:string;path:string;query:Record<string,string>}[]=[]
 const meta=(name:string,uid:string,namespace?:string)=>({name,uid,...(namespace?{namespace}:{}),resourceVersion:'7',creationTimestamp:'2026-10-22T00:00:00Z'})
 const node={apiVersion:'v1',kind:'Node',metadata:meta('worker','node-worker'),status:{allocatable:{cpu:'4',memory:'8Gi'},capacity:{cpu:'4',memory:'8Gi'},conditions:[{type:'Ready',status:'True'}],nodeInfo:{kubeletVersion:'v1.37.1',architecture:'amd64'}}}
 const pod={apiVersion:'v1',kind:'Pod',metadata:meta('web','pod-web','blue'),spec:{nodeName:'worker',containers:[{name:'app',image:'example.invalid/app:test',env:[{name:'PRIVATE',value:'PRIVATE-ENV'}],resources:{requests:{cpu:'100m',memory:'64Mi'}}}],initContainers:[{name:'init',image:'example.invalid/init:test',resources:{requests:{cpu:'500m',memory:'128Mi'}}}],overhead:{cpu:'10m',memory:'8Mi'}},status:{phase:'Running',conditions:[{type:'Ready',status:'True'}],containerStatuses:[{name:'app',ready:true,restartCount:0,state:{running:{startedAt:'2026-10-22T00:00:00Z'}}}]}}
 const data:Record<string,unknown[]>={nodes:[node],pods:[pod],namespaces:[{apiVersion:'v1',kind:'Namespace',metadata:meta('blue','ns-blue')}],events:[]}
 const kinds:Record<string,string>={nodes:'Node',pods:'Pod',namespaces:'Namespace',events:'Event'}
 let denied=false,held=false,cancelled=0
 const server=createServer((req,res)=>{
  const u=new URL(req.url??'/','http://127.0.0.1'),path=u.pathname;requests.push({method:req.method??'',path,query:Object.fromEntries(u.searchParams)})
  const send=(value:unknown,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value))}
  const fail=(code:number)=>send({apiVersion:'v1',kind:'Status',status:'Failure',code,reason:code===403?'Forbidden':'NotFound',message:'Owned fixture'},code)
  if(req.method!=='GET')return fail(405)
  if(path==='/version')return send({gitVersion:'v1.37.1',major:'1',minor:'37'})
  if(path==='/api')return send({apiVersion:'v1',kind:'APIVersions',versions:['v1']})
  if(path==='/apis')return send({apiVersion:'v1',kind:'APIGroupList',groups:[]})
  if(path==='/api/v1')return send({apiVersion:'v1',kind:'APIResourceList',groupVersion:'v1',resources:Object.entries(kinds).map(([name,kind])=>({name,kind,namespaced:name!=='nodes'&&name!=='namespaces',singularName:kind.toLowerCase(),verbs:['get','list','watch']}))})
  const match=path.match(/^\/api\/v1\/(?:namespaces\/([^/]+)\/)?([a-z]+)(?:\/([^/]+))?$/)
  if(!match)return fail(404);const [,ns,key,name]=match,kind=kinds[key];if(!kind)return fail(404)
  let rows=data[key] as {metadata:{name:string;namespace?:string}}[]??[];if(ns)rows=rows.filter(r=>r.metadata.namespace===ns)
  if(name){const row=rows.find(r=>r.metadata.name===name);return row?send(row):fail(404)}
  if(key==='pods'&&u.searchParams.get('limit')==='32'){
   if(ns!=='blue'||u.searchParams.get('fieldSelector')!=='spec.nodeName=worker')return fail(400)
   if(denied)return fail(403)
   if(held){res.on('close',()=>{cancelled++});return}
  }
  if(u.searchParams.get('watch')==='true'){
   res.writeHead(200,{'Content-Type':'application/json'});if(u.searchParams.get('sendInitialEvents')==='true'){for(const row of rows)res.write(JSON.stringify({type:'ADDED',object:row})+'\n');res.write(JSON.stringify({type:'BOOKMARK',object:{apiVersion:'v1',kind,metadata:{resourceVersion:'7',annotations:{'k8s.io/initial-events-end':'true'}}}})+'\n')}else res.write('\n');return
  }
  send({apiVersion:'v1',kind:kind+'List',metadata:{resourceVersion:'7'},items:rows})
 })
 await new Promise<void>(resolve=>server.listen(0,'127.0.0.1',resolve))
 return {port:(server.address() as {port:number}).port,requests,node,pod,denyPods:(v:boolean)=>{denied=v},holdPods:(v:boolean)=>{held=v},cancelled:()=>cancelled,close:async()=>{server.closeAllConnections();await new Promise<void>(resolve=>server.close(()=>resolve()))}}
}
