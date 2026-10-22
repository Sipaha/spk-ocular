import {createServer,type IncomingMessage,type ServerResponse} from 'node:http'
import type {Socket} from 'node:net'
import {readFileSync} from 'node:fs'

/** Owned protocol fixture; it never starts a container or executes the supplied command. */
export async function krrFixture(){
 type Object=Record<string,any>
 const sockets=new Set<Socket>(),jobs=new Map<string,Object>(),pods=new Map<string,Object>()
 const requests:{method:string;path:string;uid?:string;suspended?:boolean;fixedEngine?:boolean}[]=[]
 let complete=true,denyCleanup=false,sequence=0
 const sample=JSON.parse(readFileSync(new URL('../../internal/krr/testdata/runner-simple.json',import.meta.url),'utf8'));for(const scan of sample.scans)scan.object.namespace='tools'
 const output=JSON.stringify(sample)
 const reply=(res:ServerResponse,value:unknown,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value))}
 const failure=(res:ServerResponse,reason:string,code:number)=>reply(res,{apiVersion:'v1',kind:'Status',status:'Failure',reason,message:reason,code},code)
 const server=createServer(async(req:IncomingMessage,res:ServerResponse)=>{
  const url=new URL(req.url!,'http://fixture'),path=url.pathname,method=req.method!
  const chunks=[];for await(const chunk of req)chunks.push(chunk)
  const body=chunks.length?JSON.parse(Buffer.concat(chunks).toString()):undefined
  const record:{method:string;path:string;uid?:string;suspended?:boolean;fixedEngine?:boolean}={method,path};requests.push(record)
  if(path==='/version')return reply(res,{gitVersion:'v1.35.0'})
  if(path==='/api')return reply(res,{apiVersion:'v1',kind:'APIVersions',versions:['v1']})
  if(path==='/apis')return reply(res,{apiVersion:'v1',kind:'APIGroupList',groups:[{name:'batch',versions:[{groupVersion:'batch/v1',version:'v1'}],preferredVersion:{groupVersion:'batch/v1',version:'v1'}}]})
  if(path==='/api/v1')return reply(res,{apiVersion:'v1',kind:'APIResourceList',groupVersion:'v1',resources:[['namespaces','Namespace',false],['pods','Pod',true],['serviceaccounts','ServiceAccount',true]].map(([name,kind,namespaced])=>({name,kind,namespaced,singularName:String(kind).toLowerCase(),verbs:['get','list','watch']}))})
  if(path==='/apis/batch/v1')return reply(res,{apiVersion:'v1',kind:'APIResourceList',groupVersion:'batch/v1',resources:[{name:'jobs',kind:'Job',namespaced:true,singularName:'job',verbs:['get','list','watch','create','patch','delete']}]})
  if(path.endsWith('/selfsubjectreviews'))return reply(res,{apiVersion:'authentication.k8s.io/v1',kind:'SelfSubjectReview',status:{userInfo:{username:'fixture',groups:['system:authenticated']}}})
  if(path.endsWith('/selfsubjectaccessreviews'))return reply(res,{apiVersion:'authorization.k8s.io/v1',kind:'SelfSubjectAccessReview',status:{allowed:true}})
  if(url.searchParams.get('sendInitialEvents')==='true')return failure(res,'BadRequest',400)
  if(url.searchParams.get('watch')==='true'){res.writeHead(200,{'Content-Type':'application/json'});res.flushHeaders();const timer=setInterval(()=>res.write('\n'),1000);res.on('close',()=>clearInterval(timer));return}
  if(path==='/api/v1/namespaces')return reply(res,{apiVersion:'v1',kind:'NamespaceList',metadata:{resourceVersion:'1'},items:[{apiVersion:'v1',kind:'Namespace',metadata:{name:'tools',uid:'ns-tools',resourceVersion:'1'}}]})
  if(path==='/api/v1/namespaces/tools')return reply(res,{apiVersion:'v1',kind:'Namespace',metadata:{name:'tools',uid:'ns-tools',resourceVersion:'1'}})
  if(path==='/api/v1/namespaces/tools/serviceaccounts/reader')return reply(res,{apiVersion:'v1',kind:'ServiceAccount',metadata:{name:'reader',namespace:'tools',uid:'sa-reader',resourceVersion:'1'}})
  const jobMatch=path.match(/^\/apis\/batch\/v1\/(?:namespaces\/([^/]+)\/)?jobs(?:\/([^/]+))?$/)
  if(jobMatch){
   const [,ns,name]=jobMatch
   if(method==='POST'&&ns==='tools'&&!name){
    const job=structuredClone(body);job.metadata.uid=`job-${++sequence}`;job.metadata.resourceVersion='1'
    record.suspended=job.spec.suspend===true;record.fixedEngine=job.spec.template.spec.containers.length===1&&job.spec.template.spec.containers[0].image==='robustadev/krr@sha256:ce666bf65215ec3cbe4366df5e935ffb0c10a9aaaccaeeb879e289a0f4d67da1'&&job.spec.template.spec.containers[0].command[0]==='python'&&job.spec.template.spec.containers[0].command[1]==='-c'
    if(!record.suspended||!record.fixedEngine)return failure(res,'FixtureContractMismatch',400)
    if(jobs.has(job.metadata.name))return failure(res,'AlreadyExists',409)
    jobs.set(job.metadata.name,job);return reply(res,job,201)
   }
   const job=jobs.get(name)
   if(method==='PATCH'&&job){
    if(body[0]?.op!=='test'||body[0]?.value!==job.metadata.uid||body[1]?.value!==job.metadata.resourceVersion||body[3]?.value!==false)return failure(res,'Conflict',409)
    job.spec.suspend=false;job.metadata.resourceVersion='2'
    const pod:Object={apiVersion:'v1',kind:'Pod',metadata:{name:job.metadata.name+'-pod',namespace:'tools',uid:`pod-${sequence}`,resourceVersion:'1',labels:{'spk-ocular.dev/krr-job':job.metadata.labels['spk-ocular.dev/krr-job']},ownerReferences:[{apiVersion:'batch/v1',kind:'Job',name:job.metadata.name,uid:job.metadata.uid,controller:true}]},spec:structuredClone(job.spec.template.spec),status:{phase:complete?'Succeeded':'Running',containerStatuses:[{name:'krr',state:complete?{terminated:{exitCode:0}}:{running:{}}}]}}
    pods.set(pod.metadata.name,pod);return reply(res,job)
   }
   if(method==='DELETE'&&job){record.uid=body?.preconditions?.uid;if(denyCleanup)return failure(res,'Forbidden',403);if(record.uid!==job.metadata.uid||body.propagationPolicy!=='Foreground')return failure(res,'Conflict',409);jobs.delete(name);return reply(res,{apiVersion:'v1',kind:'Status',status:'Success'})}
   if(method==='GET'&&name)return job?reply(res,job):failure(res,'NotFound',404)
   if(method==='GET'&&!name)return reply(res,{apiVersion:'batch/v1',kind:'JobList',metadata:{resourceVersion:'1'},items:[...jobs.values()]})
  }
  const podMatch=path.match(/^\/api\/v1\/(?:namespaces\/([^/]+)\/)?pods(?:\/([^/]+))?(?:\/(log))?$/)
  if(podMatch){
   const [,ns,name,log]=podMatch,pod=pods.get(name)
   if(method==='GET'&&pod&&log){res.writeHead(200,{'Content-Type':'text/plain'});res.end('2026-10-22T00:00:00Z '+output+'\n');return}
   if(method==='DELETE'&&pod){record.uid=body?.preconditions?.uid;if(denyCleanup)return failure(res,'Forbidden',403);if(record.uid!==pod.metadata.uid)return failure(res,'Conflict',409);pods.delete(name);return reply(res,{apiVersion:'v1',kind:'Status',status:'Success'})}
   if(method==='GET'&&name)return pod?reply(res,pod):failure(res,'NotFound',404)
   if(method==='GET'&&!name)return reply(res,{apiVersion:'v1',kind:'PodList',metadata:{resourceVersion:'1'},items:[...pods.values()].filter(p=>!ns||p.metadata.namespace===ns)})
  }
  return failure(res,'NotFound',404)
 })
 server.on('connection',socket=>{sockets.add(socket);socket.on('close',()=>sockets.delete(socket))})
 await new Promise<void>(resolve=>server.listen(0,'127.0.0.1',resolve));const port=(server.address() as {port:number}).port
 return {port,requests,complete(value:boolean){complete=value},denyCleanup(value:boolean){denyCleanup=value},remaining(){return {jobs:jobs.size,pods:pods.size}},close:async()=>{for(const socket of sockets)socket.destroy();await new Promise<void>(resolve=>server.close(()=>resolve()))}}
}
