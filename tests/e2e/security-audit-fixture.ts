import {createServer} from 'node:http'
export async function auditServer(includeClusterFixtures=false,includePolicyFixtures=false,includeGlobalPolicyFixtures=false){
 const requests:{method:string;path:string;query:Record<string,string>}[]=[]
 const metadata=(name:string,uid:string,namespace='blue')=>({name,uid,namespace,resourceVersion:'7',creationTimestamp:'2026-10-01T00:00:00Z'})
 const audit=(kind:string,name:string,global=false)=>({apiVersion:'aquasecurity.github.io/v1alpha1',kind,metadata:{...metadata(name,'uid-'+name,global?'':'blue'),ownerReferences:[{apiVersion:'apps/v1',kind:'Deployment',name:'web',uid:'old-controller',controller:true}],annotations:{ignored:'PRIVATE-ANNOTATION'}},report:{scanner:{name:'Example scanner',version:'1.2',vendor:'Test'},summary:{highCount:1},checks:[{checkID:'EXT-001',title:'External configuration evidence',success:false,severity:'HIGH',category:'Reliability',description:'Producer evidence, no live scan',messages:['<script>not executed</script>','Review the declared field'],remediation:'https://example.invalid/not-opened',scope:{type:'JSONPath',value:'.spec.template.spec.containers[0]'},ignored:'PRIVATE-CHECK'},{checkID:'EXT-002',title:'External passing check',success:true,severity:'CRITICAL'},{checkID:'EXT-003',title:'External unknown result',severity:'OTHER'}]}})
 const config=audit('ConfigAuditReport','config-web'),rbac=audit('RbacAssessmentReport','rbac-web')
 // This fixture deliberately supplies an unknown check: API completeness stays partial.
 const data:Record<string,unknown[]>={configauditreports:[config],rbacassessmentreports:[rbac],namespaces:[{apiVersion:'v1',kind:'Namespace',metadata:metadata('blue','ns-blue','')}]}
 if(includeClusterFixtures){data.clusterconfigauditreports=[audit('ClusterConfigAuditReport','cluster-config',true)];data.clusterrbacassessmentreports=[audit('ClusterRbacAssessmentReport','cluster-rbac',true)]}
 const policy=(apiVersion:string,kind:string,name:string)=>({apiVersion,kind,metadata:metadata(name,'uid-'+name),source:'root-producer',scope:{apiVersion:'apps/v1',kind:'Deployment',namespace:'blue',name:'old-workload',uid:'old-subject'},configuration:{limits:{maxResults:6,statusFilter:['pass','fail','warn','error','skip']}},summary:{pass:1,fail:1,warn:1,error:1,skip:1},results:['fail','pass','warn','error','skip','future'].map((result,i)=>({source:i===4?'':'engine-'+(i%2),policy:'require-label',rule:'check-app',result,severity:i===0?'high':i===5?'info':'critical',...(i===4?{}:{scored:false,timestamp:{seconds:1700000000,nanos:0}}),category:'Reliability',message:'Policy evidence <script>not executed</script> https://example.invalid/not-opened',properties:{process:'background scan',exceptions:result==='skip'?'allowed-maintenance':'',password:'PRIVATE-PROPERTY',controlsJSON:'PRIVATE-RAW'},...(i===4?{resourceSelector:{matchLabels:{app:'old'}}}:{resources:[{apiVersion:'v1',kind:'Secret',namespace:'other',name:'descriptive-subject',uid:'old-secret'}]}),ignored:'PRIVATE-RESULT'}))})
 if(includePolicyFixtures){data.policyreports=[policy('wgpolicyk8s.io/v1beta1','PolicyReport','policy-wg')];data.reports=[policy('openreports.io/v1alpha1','Report','policy-open')]}
 if(includeGlobalPolicyFixtures){for(const [resource,api,kind,name] of [['clusterpolicyreports','wgpolicyk8s.io/v1beta1','ClusterPolicyReport','global-wg'],['clusterreports','openreports.io/v1alpha1','ClusterReport','global-open']]){const report=policy(api,kind,name);report.metadata.namespace='';report.scope={apiVersion:'v1',kind:'Node',namespace:'',name:'old-node',uid:'old-node-uid'};data[resource]=[report]}}
 const groups:Record<string,Record<string,string>>={'v1':{pods:'Pod',serviceaccounts:'ServiceAccount',namespaces:'Namespace',events:'Event'},'apps/v1':{deployments:'Deployment',replicasets:'ReplicaSet',statefulsets:'StatefulSet',daemonsets:'DaemonSet'},'batch/v1':{jobs:'Job',cronjobs:'CronJob'},'networking.k8s.io/v1':{networkpolicies:'NetworkPolicy'},'rbac.authorization.k8s.io/v1':{roles:'Role',clusterroles:'ClusterRole'},'aquasecurity.github.io/v1alpha1':{configauditreports:'ConfigAuditReport',rbacassessmentreports:'RbacAssessmentReport',vulnerabilityreports:'VulnerabilityReport'}}
 if(includeClusterFixtures)Object.assign(groups['aquasecurity.github.io/v1alpha1'],{clusterconfigauditreports:'ClusterConfigAuditReport',clusterrbacassessmentreports:'ClusterRbacAssessmentReport'})
 if(includePolicyFixtures){groups['wgpolicyk8s.io/v1beta1']={policyreports:'PolicyReport',clusterpolicyreports:'ClusterPolicyReport'};groups['openreports.io/v1alpha1']={reports:'Report',clusterreports:'ClusterReport'}}
 let mode='normal',cancelled=0
 const policyCancellationDurations:number[]=[],clusterPolicyCancellationDurations:number[]=[]
 const server=createServer((req,res)=>{
  const u=new URL(req.url??'/','http://127.0.0.1'),path=u.pathname;requests.push({method:req.method??'',path,query:Object.fromEntries(u.searchParams)})
  const send=(value:unknown,code=200)=>{res.writeHead(code,{'Content-Type':'application/json'});res.end(JSON.stringify(value))}
  const fail=(code:number)=>send({apiVersion:'v1',kind:'Status',status:'Failure',code,reason:code===403?'Forbidden':code===404?'NotFound':'MethodNotAllowed',message:'Owned fixture'},code)
  if(req.method!=='GET')return fail(405)
  if(path==='/version')return send({gitVersion:'v1.34.0',major:'1',minor:'34'})
  if(path==='/api')return send({apiVersion:'v1',kind:'APIVersions',versions:['v1']})
  if(path==='/apis')return send({apiVersion:'v1',kind:'APIGroupList',groups:Object.keys(groups).filter(g=>g!=='v1').map(gv=>{const [name,version]=gv.split('/');return {name,versions:[{groupVersion:gv,version}],preferredVersion:{groupVersion:gv,version}}})})
  const gv=path.replace(/^\/apis?\//,'')
  if(groups[gv])return send({apiVersion:'v1',kind:'APIResourceList',groupVersion:gv,resources:Object.entries(groups[gv]).map(([name,kind])=>({name,kind,namespaced:!['namespaces','clusterroles','clusterconfigauditreports','clusterrbacassessmentreports','clusterpolicyreports','clusterreports'].includes(name),singularName:kind.toLowerCase(),verbs:['get','list','watch',...(includePolicyFixtures&&['policyreports','reports','clusterpolicyreports','clusterreports'].includes(name)?['patch']:[])]}))})
  const m=path.match(/^\/(api\/v1|apis\/[^/]+\/v1(?:(?:alpha|beta)\d+)?)\/(?:namespaces\/([^/]+)\/)?([a-z]+)(?:\/([^/]+))?$/)
  if(!m)return fail(404)
  const [,api,ns,resource,name]=m,version=api==='api/v1'?'v1':api.slice(5),kind=groups[version]?.[resource]
  if(!kind)return fail(404)
  if(resource==='configauditreports'&&!name){if(mode==='denied')return fail(403);if(mode==='missing')return fail(404);if(mode==='hold'){res.on('close',()=>cancelled++);return}}
  if(resource==='clusterconfigauditreports'&&!name){if(mode==='cluster-denied')return fail(403);if(mode==='cluster-missing')return fail(404);if(mode==='cluster-hold'){res.on('close',()=>cancelled++);return}}
  if(['policyreports','reports'].includes(resource)&&u.searchParams.get('limit')==='50'){
   if(mode==='policy-denied')return fail(403);if(mode==='policy-missing')return fail(404)
   if(mode==='policy-hold'){const started=performance.now();res.on('close',()=>{cancelled++;policyCancellationDurations.push(performance.now()-started)});return}
  }
  if(['clusterpolicyreports','clusterreports'].includes(resource)&&u.searchParams.get('limit')==='50'){
   if(mode==='global-policy-denied')return fail(403);if(mode==='global-policy-missing')return fail(404)
   if(mode==='global-policy-hold'){const started=performance.now();res.on('close',()=>{cancelled++;clusterPolicyCancellationDurations.push(performance.now()-started)});return}
  }
  let rows=(data[resource]??[]) as {metadata:{name:string;namespace?:string}}[];if(ns)rows=rows.filter(r=>r.metadata.namespace===ns);if((mode==='empty'&&resource.endsWith('reports'))||(mode==='cluster-empty'&&resource.startsWith('cluster')&&resource.endsWith('reports')))rows=[]
  if(mode==='global-policy-empty'&&['clusterpolicyreports','clusterreports'].includes(resource))rows=[]
  if(mode==='policy-empty'&&['policyreports','reports'].includes(resource))rows=[]
  if(name){const row=rows.find(r=>r.metadata.name===name);return row?send(row):fail(404)}
  if(u.searchParams.get('watch')==='true'){res.writeHead(200,{'Content-Type':'application/json'});const exact=u.searchParams.get('fieldSelector')?.replace('metadata.name=','');for(const row of rows.filter(r=>!exact||r.metadata.name===exact))res.write(JSON.stringify({type:'ADDED',object:row})+'\n');res.write(JSON.stringify({type:'BOOKMARK',object:{apiVersion:version,kind,metadata:{resourceVersion:'7',annotations:{'k8s.io/initial-events-end':'true'}}}})+'\n');return}
  send({apiVersion:version,kind:kind+'List',metadata:{resourceVersion:'7'},items:rows})
 })
 await new Promise<void>(r=>server.listen(0,'127.0.0.1',r))
 return {port:(server.address() as {port:number}).port,requests,data,config,rbac,setMode:(v:string)=>{mode=v},cancelled:()=>cancelled,policyCancellationDurations,clusterPolicyCancellationDurations,close:async()=>{server.closeAllConnections();await new Promise<void>(r=>server.close(()=>r()))}}
}
