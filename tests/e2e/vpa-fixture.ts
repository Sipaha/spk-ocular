import {createServer} from 'node:http'
export async function vpaServer(){
 const requests:{method:string;path:string;accept:string;query:Record<string,string>}[]=[]
 const date=new Date().toISOString()
 const metadata=(name:string,uid:string,namespace='blue')=>({name,uid,namespace,resourceVersion:'1',generation:2,creationTimestamp:date})
 const container={name:'web',image:'example.invalid/web:test',readinessProbe:{httpGet:{path:'PRIVATE-PROBE',port:8080,httpHeaders:[{name:'Authorization',value:'PRIVATE-HEADER'}]}},env:[{name:'PRIVATE',value:'PRIVATE-ENV'}],resources:{requests:{cpu:'100m',memory:'64Mi'},limits:{memory:'128Mi'}}}
 const podSpec={containers:[container],initContainers:[{name:'setup',image:'example.invalid/setup:test'},{name:'proxy',image:'example.invalid/proxy:test',restartPolicy:'Always'}],topologySpreadConstraints:[{maxSkew:1,topologyKey:'topology.kubernetes.io/zone',whenUnsatisfiable:'DoNotSchedule',labelSelector:{matchLabels:{app:'web'}}}]}
 const deployments=['web','zero','recreate'].map((name,i)=>({apiVersion:'apps/v1',kind:'Deployment',metadata:metadata(name,'deploy-'+name),spec:{replicas:i===1?0:1,selector:{matchLabels:{app:'web'}},template:{metadata:{labels:{app:'web'}},spec:podSpec},...(i===2?{strategy:{type:'Recreate'}}:{})},status:{observedGeneration:2,replicas:1,readyReplicas:1,availableReplicas:1,updatedReplicas:1}}))
 const pdb={apiVersion:'policy/v1',kind:'PodDisruptionBudget',metadata:metadata('budget','pdb-budget'),spec:{selector:{matchLabels:{app:'web'}},minAvailable:1},status:{observedGeneration:1,disruptionsAllowed:0}}
 const hpa={apiVersion:'autoscaling/v2',kind:'HorizontalPodAutoscaler',metadata:metadata('scale','hpa-scale'),spec:{scaleTargetRef:{apiVersion:'apps/v1',kind:'Deployment',name:'web'},minReplicas:1,maxReplicas:5,metrics:[{type:'Resource',resource:{name:'cpu',target:{type:'Utilization',averageUtilization:70}}}]},status:{observedGeneration:2,currentReplicas:1,desiredReplicas:2,conditions:[{type:'ScalingActive',status:'True',observedGeneration:2}]}}
 const data:Record<string,unknown[]>={deployments,poddisruptionbudgets:[pdb],horizontalpodautoscalers:[hpa],namespaces:[{apiVersion:'v1',kind:'Namespace',metadata:metadata('blue','ns-blue','')}],pods:[],events:[],replicasets:[],statefulsets:[],daemonsets:[]}
 const groups:Record<string,Record<string,string>>={'v1':{pods:'Pod',events:'Event',namespaces:'Namespace'},'apps/v1':{deployments:'Deployment',replicasets:'ReplicaSet',statefulsets:'StatefulSet',daemonsets:'DaemonSet'},'policy/v1':{poddisruptionbudgets:'PodDisruptionBudget'},'autoscaling/v2':{horizontalpodautoscalers:'HorizontalPodAutoscaler'}}
 let denied=false,hold=false,cancelled=0
 const vpa={apiVersion:'autoscaling.k8s.io/v1',kind:'VerticalPodAutoscaler',metadata:metadata('sizing','vpa-sizing'),spec:{targetRef:{apiVersion:'apps/v1',kind:'Deployment',name:'web'},updatePolicy:{updateMode:'Off'},recommenders:[{name:'custom-recommender'}],resourcePolicy:{containerPolicies:[{containerName:'*',mode:'Auto',controlledValues:'RequestsOnly',controlledResources:['cpu','memory'],minAllowed:{cpu:'10m'},maxAllowed:{memory:'1Gi'}}]}},status:{observedGeneration:2,conditions:[{type:'RecommendationProvided',status:'True',observedGeneration:2,lastTransitionTime:date,message:'PRIVATE-MESSAGE'}],recommendation:{containerRecommendations:[{containerName:'web',target:{cpu:'250m',memory:'96Mi'},lowerBound:{cpu:'0',memory:'48Mi'},upperBound:{cpu:'1',memory:'256Mi'},uncappedTarget:{cpu:'300m',memory:'112Mi'}}]}}}
 data.verticalpodautoscalers=[vpa];groups['autoscaling.k8s.io/v1']={verticalpodautoscalers:'VerticalPodAutoscaler'}
 const server=createServer((req,res)=>{
  const u=new URL(req.url??'/','http://127.0.0.1'),path=u.pathname,accept=req.headers.accept??'';requests.push({method:req.method??'',path,accept,query:Object.fromEntries(u.searchParams)})
  const send=(data:unknown,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(data))}
  const fail=(code:number)=>send({apiVersion:'v1',kind:'Status',status:'Failure',code,reason:code===403?'Forbidden':code===404?'NotFound':'MethodNotAllowed',message:'Owned read-only fixture'},code)
  if(req.method!=='GET')return fail(405)
  if(path==='/version')return send({gitVersion:'v1.34.0',major:'1',minor:'34'})
  if(path==='/api')return send({apiVersion:'v1',kind:'APIVersions',versions:['v1']})
  if(path==='/apis')return send({apiVersion:'v1',kind:'APIGroupList',groups:Object.keys(groups).filter(v=>v!=='v1').map(gv=>{const [name,version]=gv.split('/');return {name,versions:[{groupVersion:gv,version}],preferredVersion:{groupVersion:gv,version}}})})
  const gv=path.replace(/^\/apis?\//,'')
  if(groups[gv])return send({apiVersion:'v1',kind:'APIResourceList',groupVersion:gv,resources:Object.entries(groups[gv]).map(([name,kind])=>({name,kind,namespaced:name!=='namespaces',singularName:kind.toLowerCase(),verbs:['get','list','watch','patch']}))})
  const match=path.match(/^\/(api\/v1|apis\/apps\/v1|apis\/policy\/v1|apis\/autoscaling\/v2|apis\/autoscaling.k8s.io\/v1)\/(?:namespaces\/([^/]+)\/)?([a-z]+)(?:\/([^/]+))?$/)
  if(!match)return fail(404)
  const [,api,ns,resourceName,name]=match,version=api==='api/v1'?'v1':api.slice(5),kind=groups[version]?.[resourceName]
  if(!kind)return fail(404);if(resourceName==='verticalpodautoscalers'&&denied)return fail(403);if(resourceName==='verticalpodautoscalers'&&hold){res.on('close',()=>{cancelled++});return}
  let rows=data[resourceName] as {metadata:{name:string}}[]??[];if(ns&&ns!=='blue')rows=[]
  if(name){const row=rows.find(r=>r.metadata.name===name);if(!row)return fail(404);return send(row)}
  if(u.searchParams.get('watch')==='true'){
   res.writeHead(200,{'Content-Type':'application/json'});if(u.searchParams.get('sendInitialEvents')==='true'){for(const row of rows)res.write(JSON.stringify({type:'ADDED',object:row})+'\n');res.write(JSON.stringify({type:'BOOKMARK',object:{apiVersion:version,kind,metadata:{resourceVersion:'1',annotations:{'k8s.io/initial-events-end':'true'}}}})+'\n')}else res.write('\n');return
  }
  return send({apiVersion:version,kind:kind+'List',metadata:{resourceVersion:'1'},items:rows})
 })
 await new Promise<void>(r=>server.listen(0,'127.0.0.1',r))
 return {port:(server.address() as {port:number}).port,requests,data,denyVPA:(v:boolean)=>{denied=v},holdVPA:(v:boolean)=>{hold=v},cancelled:()=>cancelled,vpa,close:async()=>{server.closeAllConnections();await new Promise<void>(r=>server.close(()=>r()))}}
}
