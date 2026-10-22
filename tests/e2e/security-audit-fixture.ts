import {createServer} from 'node:http'
export async function auditServer(includeClusterFixtures=false){
 const requests:{method:string;path:string;query:Record<string,string>}[]=[]
 const metadata=(name:string,uid:string,namespace='blue')=>({name,uid,namespace,resourceVersion:'7',creationTimestamp:'2026-10-01T00:00:00Z'})
 const audit=(kind:string,name:string,global=false)=>({apiVersion:'aquasecurity.github.io/v1alpha1',kind,metadata:{...metadata(name,'uid-'+name,global?'':'blue'),ownerReferences:[{apiVersion:'apps/v1',kind:'Deployment',name:'web',uid:'old-controller',controller:true}],annotations:{ignored:'PRIVATE-ANNOTATION'}},report:{scanner:{name:'Example scanner',version:'1.2',vendor:'Test'},summary:{highCount:1},checks:[{checkID:'EXT-001',title:'External configuration evidence',success:false,severity:'HIGH',category:'Reliability',description:'Producer evidence, no live scan',messages:['<script>not executed</script>','Review the declared field'],remediation:'https://example.invalid/not-opened',scope:{type:'JSONPath',value:'.spec.template.spec.containers[0]'},ignored:'PRIVATE-CHECK'},{checkID:'EXT-002',title:'External passing check',success:true,severity:'CRITICAL'},{checkID:'EXT-003',title:'External unknown result',severity:'OTHER'}]}})
 const config=audit('ConfigAuditReport','config-web'),rbac=audit('RbacAssessmentReport','rbac-web')
 // This fixture deliberately supplies an unknown check: API completeness stays partial.
 const data:Record<string,unknown[]>={configauditreports:[config],rbacassessmentreports:[rbac],namespaces:[{apiVersion:'v1',kind:'Namespace',metadata:metadata('blue','ns-blue','')}]}
 if(includeClusterFixtures){data.clusterconfigauditreports=[audit('ClusterConfigAuditReport','cluster-config',true)];data.clusterrbacassessmentreports=[audit('ClusterRbacAssessmentReport','cluster-rbac',true)]}
 const groups:Record<string,Record<string,string>>={'v1':{pods:'Pod',serviceaccounts:'ServiceAccount',namespaces:'Namespace',events:'Event'},'apps/v1':{deployments:'Deployment',replicasets:'ReplicaSet',statefulsets:'StatefulSet',daemonsets:'DaemonSet'},'batch/v1':{jobs:'Job',cronjobs:'CronJob'},'networking.k8s.io/v1':{networkpolicies:'NetworkPolicy'},'rbac.authorization.k8s.io/v1':{roles:'Role',clusterroles:'ClusterRole'},'aquasecurity.github.io/v1alpha1':{configauditreports:'ConfigAuditReport',rbacassessmentreports:'RbacAssessmentReport',vulnerabilityreports:'VulnerabilityReport'}}
 if(includeClusterFixtures)Object.assign(groups['aquasecurity.github.io/v1alpha1'],{clusterconfigauditreports:'ClusterConfigAuditReport',clusterrbacassessmentreports:'ClusterRbacAssessmentReport'})
 let mode='normal',cancelled=0
 const server=createServer((req,res)=>{
  const u=new URL(req.url??'/','http://127.0.0.1'),path=u.pathname;requests.push({method:req.method??'',path,query:Object.fromEntries(u.searchParams)})
  const send=(value:unknown,code=200)=>{res.writeHead(code,{'Content-Type':'application/json'});res.end(JSON.stringify(value))}
  const fail=(code:number)=>send({apiVersion:'v1',kind:'Status',status:'Failure',code,reason:code===403?'Forbidden':code===404?'NotFound':'MethodNotAllowed',message:'Owned fixture'},code)
  if(req.method!=='GET')return fail(405)
  if(path==='/version')return send({gitVersion:'v1.34.0',major:'1',minor:'34'})
  if(path==='/api')return send({apiVersion:'v1',kind:'APIVersions',versions:['v1']})
  if(path==='/apis')return send({apiVersion:'v1',kind:'APIGroupList',groups:Object.keys(groups).filter(g=>g!=='v1').map(gv=>{const [name,version]=gv.split('/');return {name,versions:[{groupVersion:gv,version}],preferredVersion:{groupVersion:gv,version}}})})
  const gv=path.replace(/^\/apis?\//,'')
  if(groups[gv])return send({apiVersion:'v1',kind:'APIResourceList',groupVersion:gv,resources:Object.entries(groups[gv]).map(([name,kind])=>({name,kind,namespaced:!['namespaces','clusterroles','clusterconfigauditreports','clusterrbacassessmentreports'].includes(name),singularName:kind.toLowerCase(),verbs:['get','list','watch']}))})
  const m=path.match(/^\/(api\/v1|apis\/[^/]+\/v1(?:alpha1)?)\/(?:namespaces\/([^/]+)\/)?([a-z]+)(?:\/([^/]+))?$/)
  if(!m)return fail(404)
  const [,api,ns,resource,name]=m,version=api==='api/v1'?'v1':api.slice(5),kind=groups[version]?.[resource]
  if(!kind)return fail(404)
  if(resource==='configauditreports'&&!name){if(mode==='denied')return fail(403);if(mode==='missing')return fail(404);if(mode==='hold'){res.on('close',()=>cancelled++);return}}
  if(resource==='clusterconfigauditreports'&&!name){if(mode==='cluster-denied')return fail(403);if(mode==='cluster-missing')return fail(404);if(mode==='cluster-hold'){res.on('close',()=>cancelled++);return}}
  let rows=(data[resource]??[]) as {metadata:{name:string;namespace?:string}}[];if(ns)rows=rows.filter(r=>r.metadata.namespace===ns);if((mode==='empty'&&resource.endsWith('reports'))||(mode==='cluster-empty'&&resource.startsWith('cluster')&&resource.endsWith('reports')))rows=[]
  if(name){const row=rows.find(r=>r.metadata.name===name);return row?send(row):fail(404)}
  if(u.searchParams.get('watch')==='true'){res.writeHead(200,{'Content-Type':'application/json'});const exact=u.searchParams.get('fieldSelector')?.replace('metadata.name=','');for(const row of rows.filter(r=>!exact||r.metadata.name===exact))res.write(JSON.stringify({type:'ADDED',object:row})+'\n');res.write(JSON.stringify({type:'BOOKMARK',object:{apiVersion:version,kind,metadata:{resourceVersion:'7',annotations:{'k8s.io/initial-events-end':'true'}}}})+'\n');return}
  send({apiVersion:version,kind:kind+'List',metadata:{resourceVersion:'7'},items:rows})
 })
 await new Promise<void>(r=>server.listen(0,'127.0.0.1',r))
 return {port:(server.address() as {port:number}).port,requests,data,config,rbac,setMode:(v:string)=>{mode=v},cancelled:()=>cancelled,close:async()=>{server.closeAllConnections();await new Promise<void>(r=>server.close(()=>r()))}}
}
