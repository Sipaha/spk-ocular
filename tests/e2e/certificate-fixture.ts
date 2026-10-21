import {createServer} from 'node:http'
export async function certificateServer(){
 const requests:{method:string;path:string;accept:string;query:Record<string,string>}[]=[]
 const now=Date.now(),date=(days:number)=>new Date(now+days*86400000).toISOString()
 const metadata=(name:string,uid:string,namespace='blue')=>({name,uid,namespace,resourceVersion:'1',generation:2,creationTimestamp:date(-10)})
 const certificates=['web-tls','expired','not-yet','missing','invalid'].map((name,i)=>({apiVersion:'cert-manager.io/v1',kind:'Certificate',metadata:metadata(name,'cert-'+name),spec:{secretName:'web-tls',issuerRef:{name:'public',kind:'Issuer'},dnsNames:['web.example','api.example'],ipAddresses:['192.0.2.1'],uris:['spiffe://example/web'],emailAddresses:['ops@example.invalid']},status:{notBefore:date(i===2?1:-30),...(i===3?{}:{notAfter:i===4?'invalid-date':date(i===1?-1:6)}),renewalTime:date(-1),conditions:[{type:'Ready',status:'True',observedGeneration:1,reason:'Ready'},{type:'Issuing',status:'True',observedGeneration:2,reason:'Renewing'}]}}))
 const issuer={apiVersion:'cert-manager.io/v1',kind:'Issuer',metadata:metadata('public','issuer-public'),spec:{selfSigned:{}},status:{conditions:[{type:'Ready',status:'True'}]}}
 const ingress={apiVersion:'networking.k8s.io/v1',kind:'Ingress',metadata:metadata('entry','ing-entry'),spec:{tls:[{secretName:'web-tls',hosts:['web.example']}],rules:[{host:'web.example',http:{paths:[]}}]},status:{loadBalancer:{ingress:[{ip:'192.0.2.2'}]}}}
 const groups=[{name:'cert-manager.io',versions:[{groupVersion:'cert-manager.io/v1',version:'v1'}],preferredVersion:{groupVersion:'cert-manager.io/v1',version:'v1'}},{name:'networking.k8s.io',versions:[{groupVersion:'networking.k8s.io/v1',version:'v1'}],preferredVersion:{groupVersion:'networking.k8s.io/v1',version:'v1'}}]
 const resource=(name:string,kind:string,namespaced=true)=>({name,kind,namespaced,singularName:kind.toLowerCase(),verbs:['get','list','watch','patch']})
 let denied=false
 const server=createServer((req,res)=>{
  const u=new URL(req.url??'/','http://127.0.0.1'),path=u.pathname,accept=req.headers.accept??'';requests.push({method:req.method??'',path,accept,query:Object.fromEntries(u.searchParams)})
  const send=(data:unknown,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(data))}
  const fail=(code:number)=>send({apiVersion:'v1',kind:'Status',status:'Failure',code,reason:code===403?'Forbidden':code===404?'NotFound':'MethodNotAllowed',message:'Owned read-only fixture'},code)
  if(req.method!=='GET')return fail(405)
  if(path==='/version')return send({gitVersion:'v1.34.0',major:'1',minor:'34'})
  if(path==='/api')return send({apiVersion:'v1',kind:'APIVersions',versions:['v1']})
  if(path==='/apis')return send({apiVersion:'v1',kind:'APIGroupList',groups})
  if(path==='/api/v1')return send({apiVersion:'v1',kind:'APIResourceList',groupVersion:'v1',resources:[resource('pods','Pod'),resource('events','Event'),resource('namespaces','Namespace',false)]})
  if(path==='/apis/cert-manager.io/v1')return send({apiVersion:'v1',kind:'APIResourceList',groupVersion:'cert-manager.io/v1',resources:[resource('certificates','Certificate'),resource('issuers','Issuer'),resource('clusterissuers','ClusterIssuer',false)]})
  if(path==='/apis/networking.k8s.io/v1')return send({apiVersion:'v1',kind:'APIResourceList',groupVersion:'networking.k8s.io/v1',resources:[resource('ingresses','Ingress')]})
  const match=path.match(/^\/(api\/v1|apis\/cert-manager.io\/v1|apis\/networking.k8s.io\/v1)\/(?:namespaces\/([^/]+)\/)?(pods|events|namespaces|certificates|issuers|ingresses)(?:\/([^/]+))?$/)
  if(!match)return fail(404)
  const [,api,ns,resourceName,name]=match
  if(resourceName==='ingresses'&&denied)return fail(403)
  const kind=({pods:'Pod',events:'Event',namespaces:'Namespace',certificates:'Certificate',issuers:'Issuer',ingresses:'Ingress'} as Record<string,string>)[resourceName]
  let rows:Record<string,unknown>[]=resourceName==='certificates'?certificates:resourceName==='issuers'?[issuer]:resourceName==='ingresses'?[ingress]:resourceName==='namespaces'?[{apiVersion:'v1',kind:'Namespace',metadata:metadata('blue','ns-blue','')}]:[]
  if(ns&&ns!=='blue')rows=[]
  if(name){const row=rows.find(r=>(r.metadata as {name:string}).name===name);if(!row)return fail(404);if(accept.includes('PartialObjectMetadata'))return send({apiVersion:'meta.k8s.io/v1',kind:'PartialObjectMetadata',metadata:row.metadata});return send(row)}
  if(u.searchParams.get('watch')==='true'){
   res.writeHead(200,{'Content-Type':'application/json'})
   if(u.searchParams.get('sendInitialEvents')==='true'){for(const row of rows)res.write(JSON.stringify({type:'ADDED',object:row})+'\n');res.write(JSON.stringify({type:'BOOKMARK',object:{apiVersion:api==='api/v1'?'v1':api.slice(5),kind,metadata:{resourceVersion:'1',annotations:{'k8s.io/initial-events-end':'true'}}}})+'\n')}else res.write('\n')
   return
  }
  return send({apiVersion:api==='api/v1'?'v1':api.slice(5),kind:kind+'List',metadata:{resourceVersion:'1'},items:rows})
 })
 await new Promise<void>(r=>server.listen(0,'127.0.0.1',r))
 return {port:(server.address() as {port:number}).port,requests,denyConsumers:(v:boolean)=>{denied=v},close:async()=>{server.closeAllConnections();await new Promise<void>(r=>server.close(()=>r()))}}
}
