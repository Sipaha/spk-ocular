import {createServer} from 'node:http'
import {gzipSync} from 'node:zlib'
export async function helmDiagnosisServer(){
 const requests:{method:string;path:string;accept:string;query:Record<string,string>}[]=[]
 const metadata=(name:string,uid:string,namespace='workloads')=>({name,uid,namespace,resourceVersion:'7',generation:2,creationTimestamp:'2026-10-20T10:00:00Z'})
 const hook='apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: check\n'
 const manifest=(revision:number)=>`apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: demo\ndata:\n  greeting: revision-${revision}\n---\napiVersion: v1\nkind: List\nitems:\n- apiVersion: v1\n  kind: Secret\n  metadata:\n    name: credentials\n    annotations:\n      hidden: PRIVATE-ANNOTATION\n  stringData:\n    password: PRIVATE-PASSWORD-${revision}\n`
 const stored=(name:string,revision:number,status:string,rollback=0)=>{
  const r={name,namespace:'workloads',version:revision,chart:{metadata:{apiVersion:'v2',name:'demo',version:'1.0.0',appVersion:'1'},values:{}},config:{},manifest:manifest(revision),info:{status,last_deployed:'2026-10-21T10:00:00Z',rollback_revision:rollback},hooks:[{name:'check',kind:'Job',manifest:hook,events:['post-upgrade'],delete_policies:['before-hook-creation'],last_run:{started_at:'2026-10-21T10:00:00Z',completed_at:'2026-10-21T10:01:00Z',phase:'Failed'}}]}
  return {apiVersion:'v1',kind:'Secret',metadata:{...metadata(`sh.helm.release.v1.${name}.v${revision}`,`release-${name}-${revision}`,'storage'),labels:{owner:'helm',name,status,version:String(revision)}},type:'helm.sh/release.v1',data:{release:Buffer.from(gzipSync(Buffer.from(JSON.stringify(r))).toString('base64')).toString('base64')}}
 }
 const releases=[stored('demo',1,'superseded'),stored('demo',2,'failed'),stored('demo',3,'pending-upgrade'),stored('demo',4,'deployed',1),stored('first-install',1,'failed'),stored('missing-hook',1,'failed')]
 const job={apiVersion:'batch/v1',kind:'Job',metadata:metadata('check','current-job'),spec:{template:{spec:{containers:[{name:'hook',image:'fixture',envFrom:[{secretRef:{name:'dependency-never-read'}}]}]}}},status:{failed:1,conditions:[{type:'Failed',status:'True',reason:'BackoffLimitExceeded',message:'Current Job failed'}]}}
 const pod={apiVersion:'v1',kind:'Pod',metadata:{...metadata('check-pod','current-pod'),ownerReferences:[{apiVersion:'batch/v1',kind:'Job',name:'check',uid:'current-job',controller:true}]},spec:{containers:[{name:'hook',image:'fixture',envFrom:[{secretRef:{name:'dependency-never-read'}}]}]},status:{phase:'Failed',containerStatuses:[{name:'hook',ready:false,restartCount:0,state:{terminated:{reason:'Error',exitCode:1,message:'Current container failed'}}}]}}
 const hr={apiVersion:'helm.toolkit.fluxcd.io/v2',kind:'HelmRelease',metadata:metadata('managed-demo','flux-demo','controllers'),spec:{chartRef:{kind:'OCIRepository',name:'demo-source'},valuesFrom:[{kind:'Secret',name:'dependency-never-read'}]},status:{observedGeneration:2,storageNamespace:'storage',history:[{name:'demo',namespace:'workloads',version:2}],conditions:[{type:'Ready',status:'False',observedGeneration:2,reason:'UpgradeFailed',message:'Controller reported a failed upgrade'}]}}
 const event={apiVersion:'v1',kind:'Event',metadata:metadata('check-failed','event-current'),involvedObject:{apiVersion:'batch/v1',kind:'Job',name:'check',namespace:'workloads',uid:'current-job'},type:'Warning',reason:'BackoffLimitExceeded',message:'Current Job reached retry limit'}
 const groups:Record<string,Record<string,string>>={'v1':{pods:'Pod',events:'Event',namespaces:'Namespace',secrets:'Secret'},'batch/v1':{jobs:'Job'},'helm.toolkit.fluxcd.io/v2':{helmreleases:'HelmRelease'}}
 let denyEvents=false,denyFlux=false,missing=false,replaced=false
 const server=createServer((req,res)=>{
  const url=new URL(req.url??'/','http://127.0.0.1'),path=url.pathname,accept=req.headers.accept??'';requests.push({method:req.method??'',path,accept,query:Object.fromEntries(url.searchParams)})
  const send=(data:unknown,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(data))}
  const fail=(code:number)=>send({apiVersion:'v1',kind:'Status',status:'Failure',code,reason:code===403?'Forbidden':code===404?'NotFound':'MethodNotAllowed',message:'Owned read-only fixture'},code)
  if(req.method!=='GET')return fail(405)
  if(path==='/version')return send({gitVersion:'v1.34.0',major:'1',minor:'34'})
  if(path==='/api')return send({apiVersion:'v1',kind:'APIVersions',versions:['v1']})
  if(path==='/apis')return send({apiVersion:'v1',kind:'APIGroupList',groups:Object.keys(groups).filter(g=>g!=='v1').map(g=>{const [name,version]=g.split('/');return{name,versions:[{groupVersion:g,version}],preferredVersion:{groupVersion:g,version}}})})
  const gv=path.replace(/^\/apis?\//,'')
  if(groups[gv])return send({apiVersion:'v1',kind:'APIResourceList',groupVersion:gv,resources:Object.entries(groups[gv]).map(([name,kind])=>({name,kind,singularName:kind.toLowerCase(),namespaced:name!=='namespaces',verbs:['get','list','watch']}))})
  const match=path.match(/^\/(api\/v1|apis\/batch\/v1|apis\/helm.toolkit.fluxcd.io\/v2)\/(?:namespaces\/([^/]+)\/)?(pods|events|namespaces|secrets|jobs|helmreleases)(?:\/([^/]+))?$/)
  if(!match)return fail(404)
  const [,route,ns,resource,name]=match,api=route==='api/v1'?'v1':route.slice(5),kind=groups[api][resource]
  if(resource==='events'&&denyEvents)return fail(403)
  if(resource==='helmreleases'&&denyFlux)return fail(403)
  let rows:Record<string,unknown>[]=resource==='secrets'?releases:resource==='jobs'?(missing?[]:[replaced?{...job,metadata:{...job.metadata,uid:'replacement-job'}}:job]):resource==='pods'?[pod]:resource==='events'?[event]:resource==='helmreleases'?[hr]:resource==='namespaces'?['storage','workloads','controllers'].map(n=>({apiVersion:'v1',kind:'Namespace',metadata:metadata(n,'ns-'+n,'')})):[]
  if(ns)rows=rows.filter(r=>(r.metadata as {namespace:string}).namespace===ns)
  const labels=url.searchParams.get('labelSelector');if(labels)for(const pair of labels.split(',')){const [key,value]=pair.split('=');rows=rows.filter(r=>(r.metadata as {labels?:Record<string,string>}).labels?.[key]===value)}
  if(name){const row=rows.find(r=>(r.metadata as {name:string}).name===name);if(!row)return fail(404);if(accept.includes('PartialObjectMetadata'))return send({apiVersion:'meta.k8s.io/v1',kind:'PartialObjectMetadata',metadata:row.metadata});return send(row)}
  if(url.searchParams.get('watch')==='true'){
   res.writeHead(200,{'Content-Type':'application/json'});if(url.searchParams.get('sendInitialEvents')==='true'){for(const row of rows)res.write(JSON.stringify({type:'ADDED',object:row})+'\n');res.write(JSON.stringify({type:'BOOKMARK',object:{apiVersion:api,kind,metadata:{resourceVersion:'7',annotations:{'k8s.io/initial-events-end':'true'}}}})+'\n')}else res.write('\n');return
  }
  send({apiVersion:api,kind:kind+'List',metadata:{resourceVersion:'7'},items:rows})
 })
 await new Promise<void>(r=>server.listen(0,'127.0.0.1',r))
 return {port:(server.address() as {port:number}).port,data:{secrets:releases,jobs:[job],pods:[pod],events:[event],helmreleases:[hr],namespaces:['storage','workloads','controllers'].map(n=>({apiVersion:'v1',kind:'Namespace',metadata:metadata(n,'ns-'+n,'')}))},requests,denyFlux:(v:boolean)=>{denyFlux=v},denyEvents:(v:boolean)=>{denyEvents=v},missing:(v:boolean)=>{missing=v},replace:(v:boolean)=>{replaced=v},close:async()=>{server.closeAllConnections();await new Promise<void>(r=>server.close(()=>r()))}}
}
