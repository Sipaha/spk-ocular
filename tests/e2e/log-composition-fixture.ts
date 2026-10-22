import {createServer,type ServerResponse} from 'node:http'

/** Owned read-only Kubernetes protocol fixture. Mutations are always rejected. */
export async function compositionServer(){
 const requests:{method:string;path:string;query:Record<string,string>}[]=[]
 const now=Date.now(),stamp=(n:number)=>new Date(now-3000+n*1000).toISOString()
 const meta=(name:string,uid:string,namespace='blue')=>({name,uid,namespace,resourceVersion:'1',creationTimestamp:stamp(0)})
 const owner=(kind:string,name:string,uid:string)=>({apiVersion:'apps/v1',kind,name,uid,controller:true})
 const container={name:'app',image:'example.invalid/log-fixture:v1'}
 const deployments=['web','blocked','empty'].map(name=>({apiVersion:'apps/v1',kind:'Deployment',metadata:meta(name,'deploy-'+name),spec:{replicas:1,selector:{matchLabels:{app:name}},template:{metadata:{labels:{app:name}},spec:{containers:[container]}}},status:{replicas:1,readyReplicas:1}}))
 const rs={apiVersion:'apps/v1',kind:'ReplicaSet',metadata:{...meta('web-rs','rs-web'),ownerReferences:[owner('Deployment','web','deploy-web')]},spec:{replicas:1,selector:{matchLabels:{app:'web'}},template:{spec:{containers:[container]}}}}
 const pod=(namespace:string,name:string,uid:string)=>({apiVersion:'v1',kind:'Pod',metadata:{...meta(name,uid,namespace),ownerReferences:namespace==='blue'?[owner('ReplicaSet','web-rs','rs-web')]:[]},spec:{containers:[container],restartPolicy:'Always'},status:{phase:'Running',containerStatuses:[{name:'app',containerID:'fixture://'+uid,ready:true,restartCount:0,state:{running:{startedAt:stamp(0)}}}]}})
 const pods=[pod('blue','web-a','pod-a'),pod('green','web-a','pod-green')]
 const data:Record<string,unknown[]>={deployments,replicasets:[rs],pods,namespaces:['blue','green'].map(name=>({apiVersion:'v1',kind:'Namespace',metadata:meta(name,'ns-'+name,'')})),statefulsets:[],daemonsets:[],events:[]}
 const groups:Record<string,Record<string,string>>={'v1':{pods:'Pod',namespaces:'Namespace',events:'Event'},'apps/v1':{deployments:'Deployment',replicasets:'ReplicaSet',statefulsets:'StatefulSet',daemonsets:'DaemonSet'}}
 const live=new Map<ServerResponse,string>(),watches=new Set<ServerResponse>()
 const history:Record<string,{ts:string;text:string}[]>=Object.fromEntries(['blue','green'].map(ns=>[ns,[{ts:stamp(1),text:ns+' ERROR first'},{ts:stamp(2),text:ns+' INFO second'}]]))
 let blocked=true
 const server=createServer((req,res)=>{
  const u=new URL(req.url??'/','http://127.0.0.1'),path=u.pathname
  requests.push({method:req.method??'',path,query:Object.fromEntries(u.searchParams)})
  const send=(body:unknown,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(body))}
  const fail=(code:number)=>send({apiVersion:'v1',kind:'Status',status:'Failure',code,reason:code===403?'Forbidden':code===404?'NotFound':'MethodNotAllowed',message:'Owned read-only log fixture'},code)
  if(req.method!=='GET')return fail(405)
  if(path==='/version')return send({gitVersion:'v1.34.0',major:'1',minor:'34'})
  if(path==='/api')return send({apiVersion:'v1',kind:'APIVersions',versions:['v1']})
  if(path==='/apis')return send({apiVersion:'v1',kind:'APIGroupList',groups:[{name:'apps',versions:[{groupVersion:'apps/v1',version:'v1'}],preferredVersion:{groupVersion:'apps/v1',version:'v1'}}]})
  const gv=path.replace(/^\/apis?\//,'')
  if(groups[gv])return send({apiVersion:'v1',kind:'APIResourceList',groupVersion:gv,resources:Object.entries(groups[gv]).map(([name,kind])=>({name,kind,namespaced:name!=='namespaces',singularName:kind.toLowerCase(),verbs:['get','list','watch']}))})
  const log=path.match(/^\/api\/v1\/namespaces\/(blue|green)\/pods\/web-a\/log$/)
  if(log){
   res.writeHead(200,{'Content-Type':'text/plain'})
   const since=u.searchParams.get('sinceTime'),records=history[log[1]].filter(r=>!since||Date.parse(r.ts)>=Date.parse(since)).map(r=>`${r.ts} ${r.text}\n`)
   const tail=Number(u.searchParams.get('tailLines')??-1)
   if(tail!==0)res.write((tail<0?records:records.slice(-tail)).join(''))
   if(u.searchParams.get('follow')==='true'){live.set(res,log[1]);res.on('close',()=>live.delete(res));return}
   res.end();return
  }
  const match=path.match(/^\/(api\/v1|apis\/apps\/v1)\/(?:namespaces\/([^/]+)\/)?([a-z]+)(?:\/([^/]+))?$/)
  if(!match)return fail(404)
  const [,api,ns,resource,name]=match,version=api==='api/v1'?'v1':'apps/v1',kind=groups[version]?.[resource]
  if(!kind)return fail(404)
  if(name==='blocked'&&blocked)return fail(403)
  let rows=(data[resource]??[]) as {metadata:{name:string;namespace?:string}}[]
  if(ns)rows=rows.filter(r=>r.metadata.namespace===ns)
  const field=u.searchParams.get('fieldSelector');if(field?.startsWith('metadata.name='))rows=rows.filter(r=>r.metadata.name===field.slice(14))
  if(name){const row=rows.find(r=>r.metadata.name===name);return row?send(row):fail(404)}
  if(u.searchParams.get('watch')==='true'){
   res.writeHead(200,{'Content-Type':'application/json'});watches.add(res);res.on('close',()=>watches.delete(res))
   if(u.searchParams.get('sendInitialEvents')==='true'){
    for(const row of rows)res.write(JSON.stringify({type:'ADDED',object:row})+'\n')
    res.write(JSON.stringify({type:'BOOKMARK',object:{apiVersion:version,kind,metadata:{resourceVersion:'1',annotations:{'k8s.io/initial-events-end':'true'}}}})+'\n')
   }else res.write('\n')
   return
  }
  send({apiVersion:version,kind:kind+'List',metadata:{resourceVersion:'1'},items:rows})
 })
 await new Promise<void>(r=>server.listen(0,'127.0.0.1',r))
 return {port:(server.address() as {port:number}).port,requests,data,live,denyRoot:(v:boolean)=>{blocked=v},append:(text:string)=>{const ts=new Date().toISOString();for(const rows of Object.values(history))rows.push({ts,text});for(const [res] of live)res.write(ts+' '+text+'\n')},close:async()=>{server.closeAllConnections();await new Promise<void>(r=>server.close(()=>r()))}}
}
