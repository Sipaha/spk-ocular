import type {ConceptId} from './catalog'
import type {Row} from '../api/types'

export const topics=['network','probes','runtime','config','resources'] as const
export type Topic=typeof topics[number]
export const cases={network:['selector','port'],probes:['readiness','startup'],runtime:['image','command'],config:['environment','secret'],resources:['requests','memory']} as const
export type CaseId=typeof cases[Topic][number]
export type View='properties'|'yaml'|'events'|'logs'
export type Diagnosis='selector'|'port'|'readiness'|'startup'|'image'|'command'|'environment'|'secret'|'requests'|'memory'|'credentials'|'usage'|'replicas'
export interface ExerciseState {generation:number;step:number;selected:string|null;view:View;seen:string[];diagnosis:string;repair:string;fixed:boolean;hint:boolean;result:'evidence'|'diagnosis'|'repair'|'observe'|null;complete:boolean}
export const initialExercise:ExerciseState={generation:0,step:0,selected:null,view:'properties',seen:[],diagnosis:'',repair:'',fixed:false,hint:false,result:null,complete:false}
const observationKeys={
 'app.listenPort':'listener','endpoints.ready':'readyAddresses','simulation.request':'request','app.readinessPath':'readyPath','app.coldStartSeconds':'coldStart','app.startedPath':'startedPath',
 'container.startedBeforeConfigUpdate':'oldEnvironment','container.env.MODE':'environment','keys':'secretKeys','validated.app.cpuRequest':'validatedCPU',
 'allocated.requests.cpu':'allocatedCPU','remaining.requests.cpu':'remainingCPU','observed.usage.cpu':'usageCPU','validated.app.workingSet':'workingSet','approved.app.memoryBudget':'approvedMemory','remaining.requests.memory':'remainingMemory',
} as const
export type ObservationKey=typeof observationKeys[keyof typeof observationKeys]
const fieldPaths:Record<string,string>={restartCount:'status.containerStatuses[0].restartCount','state.waiting.reason':'status.containerStatuses[0].state.waiting.reason','lastState.terminated.exitCode':'status.containerStatuses[0].lastState.terminated.exitCode','lastState.terminated.reason':'status.containerStatuses[0].lastState.terminated.reason','allocatable.cpu':'status.allocatable.cpu','conditions.ready':'endpoints[0].conditions.ready','status.conditions.Ready':'status.conditions[type=Ready].status','status.conditions.PodScheduled':'status.conditions[type=PodScheduled].status','conditions.MemoryPressure':'status.conditions[type=MemoryPressure].status'}
export interface EvidenceObject {id:string;uid:string;name:string;kind:ConceptId;properties:[string,string][];observations:[ObservationKey,string][];yaml:string;events:string;logs:string|null}
export interface Repair {id:string;patch:string}
export interface Exercise {token:string;caseId:CaseId;check:boolean;name:string;objects:EvidenceObject[];resultEvidence:string;required:string[];diagnoses:Diagnosis[];repairs:Repair[];correctRepair:string}
export type ExerciseAction={type:'reset'}|{type:'next';token:string}|{type:'select';token:string;id:string}|{type:'view';token:string;view:View}|{type:'diagnosis';token:string;value:string}|{type:'repair';token:string;value:string}|{type:'hint';token:string}|{type:'apply';token:string}
const api=(kind:ConceptId)=>kind==='services'||kind==='pods'||kind==='configmaps'||kind==='secrets'||kind==='nodes'?'v1':kind==='discovery.k8s.io/endpointslices'?'discovery.k8s.io/v1':'apps/v1'
const names:Partial<Record<ConceptId,string>>={services:'Service',pods:'Pod',configmaps:'ConfigMap',secrets:'Secret',nodes:'Node','apps/deployments':'Deployment','apps/replicasets':'ReplicaSet','discovery.k8s.io/endpointslices':'EndpointSlice'}
function rotate<T>(items:readonly T[],shift:number):T[]{return [...items.slice(shift%items.length),...items.slice(0,shift%items.length)]}
/** Bounded authored observations, not Kubernetes execution or network emulation. */
export function exercise(topic:Topic,state:ExerciseState):Exercise {
 const caseId=cases[topic][state.step%2],check=state.step>=2,token=`${topic}:${state.generation}:${state.step}`
 const root=check?'orders':'web',name=`${root}-${state.generation+1}`
 const objects:EvidenceObject[]=[],required:string[]=[]
 const object=(id:string,kind:ConceptId,title:string,body:string,properties:[string,string][],events='',logs:string|null=null)=>{
  const uid=`${token}:${id}${id==='pod'&&state.fixed&&topic!=='network'?':new':''}`
  const observations:[ObservationKey,string][]=properties.filter(([key])=>Object.hasOwn(observationKeys,key)).map(([key,value])=>[observationKeys[key as keyof typeof observationKeys],value])
  const fields=properties.filter(([key])=>!Object.hasOwn(observationKeys,key)).map(([key,value])=>[fieldPaths[key]??key,value] as [string,string])
  const obj={id,uid,name:title,kind,properties:fields,observations,yaml:`apiVersion: ${api(kind)}\nkind: ${names[kind]}\nmetadata:\n  name: ${title}\n${kind==='nodes'?'':'  namespace: lesson\n'}  uid: ${uid}\n${body}`,events,logs};objects.push(obj);return obj
 }
 const repair=(id:string,patch:string):Repair=>({id,patch})
 const fixed=state.fixed,app=name,port=check?9090:8080
 const readyPath=check?'/health/ready':'/ready',coldStart=check?55:40,imageTag=check?'release-7':'v2',argument=check?'--http':'--serve',badArgument=check?'--httpp':'--servve'
 const mode=check?'live':'production',oldMode=check?'canary':'staging',secretKey=check?'API_TOKEN':'PASSWORD',envKey=check?'LOGIN_TOKEN':'DB_PASSWORD'
 const cpuRequest=check?'500m':'250m',oversizedCpu=check?'3000m':'2000m',memoryLimit=check?'128Mi':'64Mi',workingSet=check?'192Mi':'128Mi',memoryBudget=check?'384Mi':'256Mi'
 let diagnoses:Diagnosis[]=[],repairs:Repair[]=[],correctRepair=''
 const workload=(container:string,podProperties:[string,string][],events:string,logs:string|null)=>{
  object('deployment','apps/deployments',name,`spec:\n  replicas: 1\n  selector:\n    matchLabels: {app: ${app}}\n  template:\n    metadata:\n      labels: {app: ${app}}\n    spec:\n      containers:\n      - name: app\n${container}`, [['spec.replicas','1'],['spec.template.metadata.labels.app',app]])
  object('pod','pods',`${name}-${fixed?'new':'a'}`,`  labels: {app: ${app}}\nspec:\n  containers:\n  - name: app\n${container.replace(/^ {4}/gm,'')}`,podProperties,events,logs)
 }
 const containerField='Deployment.spec.template.spec.containers[app]'
 switch(caseId){
  case 'selector':case 'port':{
   const selector=caseId==='selector'&&!fixed?'unrelated':app,target=caseId==='port'&&!fixed?port+1:port
   object('service','services',name,`spec:\n  selector: {app: ${selector}}\n  ports:\n  - port: 80\n    targetPort: ${target}\n`,[['spec.selector.app',selector],['spec.ports[0].targetPort',String(target)]])
   object('pod','pods',`${name}-a`,`spec:\n  containers:\n  - name: app\n    image: example.invalid/app:v1\n    ports: [{containerPort: ${port}}]\n`,[['metadata.labels.app',app],['status.conditions.Ready','True'],['app.listenPort',String(port)]],'',`[simulation] listening on :${port}`)
   // Labels are explicit evidence; include them in the displayed manifest too.
   objects[1].yaml=objects[1].yaml.replace('spec:\n',`  labels: {app: ${app}}\nspec:\n`)
   object('endpoints','discovery.k8s.io/endpointslices',`${name}-slice`,`addressType: IPv4\nports: [{port: ${target}}]\nendpoints: ${selector===app?'[{addresses: ["192.0.2.10"], conditions: {ready: true}}]':'[]'}\n`,[['endpoints.ready',selector===app?'192.0.2.10':'[]'],['ports[0].port',String(target)],['simulation.request',fixed?'HTTP 200':'connection failed']])
   required.push('service:yaml','pod:yaml','endpoints:properties');diagnoses=['selector','port','replicas']
   repairs=[repair('selector',`Service.spec.selector.app = ${app}`),repair('port',`Service.spec.ports[0].targetPort = ${port}`),repair('replicas','Deployment.spec.replicas = 3')];correctRepair=caseId
   break
  }
  case 'readiness':{
   const path=fixed?readyPath:'/wrong'
   workload(`        image: example.invalid/app:v1\n        readinessProbe:\n          httpGet: {path: ${path}, port: ${port}}\n        livenessProbe:\n          httpGet: {path: /live, port: ${port}}\n`,[['status.phase','Running'],['status.conditions.Ready',fixed?'True':'False'],['restartCount','0'],['app.readinessPath',readyPath]],fixed?'Normal Ready':'Warning Unhealthy: readiness HTTP 404',`[simulation] ${readyPath} -> 200; /wrong -> 404; /live -> 200`)
   object('endpoints','discovery.k8s.io/endpointslices',`${name}-slice`,`addressType: IPv4\nports: [{port: ${port}}]\nendpoints:\n- addresses: ["192.0.2.10"]\n  conditions: {ready: ${fixed}}\n`,[['conditions.ready',String(fixed)]])
   required.push('deployment:yaml','pod:events','pod:logs');diagnoses=['readiness','startup','replicas'];repairs=[repair('readiness',`${containerField}.readinessProbe.httpGet.path = ${readyPath}`),repair('liveness',`${containerField}.livenessProbe.httpGet.path = /wrong`),repair('replicas','Deployment.spec.replicas = 3')];correctRepair='readiness';break
  }
  case 'startup':{
   workload(`        image: example.invalid/app:v1\n${fixed?'        startupProbe:\n          httpGet: {path: /started, port: '+port+'}\n          periodSeconds: 5\n          failureThreshold: 12\n':''}        livenessProbe:\n          httpGet: {path: /live, port: ${port}}\n          periodSeconds: 5\n          failureThreshold: 3\n`,[['status.phase','Running'],['restartCount',fixed?'0':'4'],['app.coldStartSeconds',String(coldStart)],['app.startedPath','/started']],fixed?`Normal Started: startup succeeded after ${coldStart}s`:'Warning Unhealthy: liveness failed during initialization; Killing',`[simulation] initialization requires ${coldStart}s; /started becomes 200 when initialized; /live unavailable before initialization`)
   required.push('deployment:yaml','pod:events','pod:logs');diagnoses=['startup','readiness','image'];repairs=[repair('startup',`${containerField}.startupProbe = {httpGet: {path: /started, port: ${port}}, periodSeconds: 5, failureThreshold: 12}`),repair('readiness',`${containerField}.readinessProbe.httpGet.path = /started`),repair('threshold',`${containerField}.livenessProbe.failureThreshold = 1`)];correctRepair='startup';break
  }
  case 'image':{
   const image=`example.invalid/${app}:${fixed?imageTag:'missing'}`
   workload(`        image: ${image}\n`,[['status.phase',fixed?'Running':'Pending'],['state.waiting.reason',fixed?'—':'ImagePullBackOff'],['restartCount','0']],fixed?'Normal Pulled; Started':'Warning Failed: manifest unknown; BackOff pulling image',fixed?'[simulation] app ready':null)
   object('catalog','configmaps','approved-images',`data:\n  image: example.invalid/${app}:${imageTag}\n`,[['data.image',`example.invalid/${app}:${imageTag}`]])
   required.push('deployment:yaml','pod:events','catalog:properties');diagnoses=['image','command','credentials'];repairs=[repair('image',`${containerField}.image = example.invalid/${app}:${imageTag}`),repair('always',`${containerField}.imagePullPolicy = Always`),repair('credential','Deployment.spec.template.spec.imagePullSecrets = [{name: registry-token}]')];correctRepair='image';break
  }
  case 'command':{
   workload(`        image: example.invalid/app:v2\n        args: ["${fixed?argument:badArgument}"]\n`,[['status.phase','Running'],['state.waiting.reason',fixed?'—':'CrashLoopBackOff'],['lastState.terminated.exitCode',fixed?'—':'2'],['restartCount',fixed?'0':'5']],fixed?'Normal Started':'Normal Pulled; Warning BackOff restarting failed container',fixed?'[simulation] app ready':`[previous container, simulation] unknown option ${badArgument}; supported option: ${argument}; exit 2`)
   required.push('deployment:yaml','pod:events','pod:logs');diagnoses=['command','image','memory'];repairs=[repair('command',`${containerField}.args = ["${argument}"]`),repair('image',`${containerField}.imagePullPolicy = Always`),repair('restart','Deployment.spec.replicas = 3')];correctRepair='command';break
  }
  case 'environment':{
   workload(`        image: example.invalid/app:v2\n        env:\n        - name: MODE\n          valueFrom:\n            configMapKeyRef: {name: ${name}-config, key: mode}\n`,[['status.phase','Running'],['container.startedBeforeConfigUpdate',String(!fixed)],['container.env.MODE',fixed?mode:oldMode]],fixed?'Normal SuccessfulCreate: new container after reviewed rollout':'Normal Started: MODE captured before ConfigMap update',`[simulation] MODE=${fixed?mode:oldMode}`)
   object('config','configmaps',`${name}-config`,`data:\n  mode: ${mode}\n`,[['data.mode',mode]])
   if(fixed)objects[0].yaml=objects[0].yaml.replace('      labels:', '      annotations: {lesson-revision: "2"}\n      labels:')
   required.push('deployment:yaml','config:yaml','pod:properties');diagnoses=['environment','secret','image'];repairs=[repair('rollout','Deployment.spec.template.metadata.annotations.lesson-revision = "2"'),repair('rewrite',`ConfigMap[${name}-config].data.mode = ${mode}`),repair('port',`${containerField}.ports = [{containerPort: 80}]`)];correctRepair='rollout';break
  }
  case 'secret':{
   workload(`        image: example.invalid/app:v2\n        env:\n        - name: ${envKey}\n          valueFrom:\n            secretKeyRef: {name: ${name}-credentials, key: ${fixed?secretKey:envKey}}\n`,[['status.phase',fixed?'Running':'Pending'],['state.waiting.reason',fixed?'—':'CreateContainerConfigError']],fixed?'Normal Started':`Warning Failed: key ${envKey} not found in Secret`,fixed?'[simulation] configured; no Secret values recorded':null)
   object('secret','secrets',`${name}-credentials`,`type: Opaque\n# Key names: ${secretKey}. Values are not part of this lesson.\n`,[['type','Opaque'],['keys',secretKey]])
   required.push('deployment:yaml','secret:properties','pod:events');diagnoses=['secret','credentials','environment'];repairs=[repair('secret',`${containerField}.env[${envKey}].valueFrom.secretKeyRef.key = ${secretKey}`),repair('optional',`${containerField}.env[${envKey}].valueFrom.secretKeyRef.optional = true`),repair('credential','Deployment.spec.template.spec.imagePullSecrets = [{name: registry-token}]')];correctRepair='secret';break
  }
  case 'requests':{
   workload(`        image: example.invalid/app:v2\n        resources:\n          requests: {cpu: ${fixed?cpuRequest:oversizedCpu}, memory: 128Mi}\n          limits: {cpu: "${check?4:2}", memory: 256Mi}\n`,[['status.phase',fixed?'Running':'Pending'],['status.conditions.PodScheduled',fixed?'True':'False'],['validated.app.cpuRequest',cpuRequest]],fixed?'Normal Scheduled':'Warning FailedScheduling: Insufficient cpu',fixed?'[simulation] app ready':null)
   object('node','nodes','lesson-node',`status:\n  allocatable: {cpu: "${check?4:2}", memory: 2Gi}\n`,[['allocatable.cpu',check?'4000m':'2000m'],['allocated.requests.cpu',check?'3250m':'1500m'],['remaining.requests.cpu',check?'750m':'500m'],['observed.usage.cpu','100m']])
   required.push('deployment:yaml','pod:events','node:properties');diagnoses=['requests','usage','memory'];repairs=[repair('requests',`${containerField}.resources.requests.cpu = ${cpuRequest}`),repair('limit',`${containerField}.resources.limits.cpu = "4"`),repair('increase',`${containerField}.resources.requests.cpu = 3000m`)];correctRepair='requests';break
  }
  case 'memory':{
   workload(`        image: example.invalid/app:v2\n        resources:\n          requests: {cpu: 250m, memory: 64Mi}\n          limits: {cpu: "1", memory: ${fixed?memoryBudget:memoryLimit}}\n`,[['status.phase','Running'],['lastState.terminated.reason',fixed?'—':'OOMKilled'],['restartCount',fixed?'0':'3'],['validated.app.workingSet',workingSet],['approved.app.memoryBudget',memoryBudget]],fixed?'Normal Started':'Warning BackOff restarting container after OOMKilled',fixed?`[simulation] working set ${workingSet}; app ready`:`[previous container, simulation] working set ${workingSet} exceeds allowed ${memoryLimit}`)
   object('node','nodes','lesson-node','status:\n  allocatable: {memory: 2Gi}\n',[['remaining.requests.memory','512Mi'],['conditions.MemoryPressure','False']])
   required.push('deployment:yaml','pod:properties','node:properties');diagnoses=['memory','requests','command'];repairs=[repair('memory',`${containerField}.resources.limits.memory = ${memoryBudget}`),repair('cpu',`${containerField}.resources.limits.cpu = "2"`),repair('reduce',`${containerField}.resources.limits.memory = 32Mi`)];correctRepair='memory';break
  }
 }
 // Workload changes produce a new local Pod identity, rather than teaching edits
 // to immutable live Pod fields. These authored steps do not run a controller.
 return {token,caseId,check,name,objects,resultEvidence:topic==='network'?'endpoints:properties':'pod:properties',required,diagnoses:rotate(diagnoses,state.generation+state.step),repairs:rotate(repairs,state.generation+state.step+1),correctRepair}
}
export function exerciseRows(e:Exercise):Row[]{return e.objects.map(o=>({id:o.id,ref:{provider:'learning',target:'local-path',scope:'lesson',kind:o.kind,name:o.name,uid:o.uid},cells:[{text:o.name},{text:names[o.kind]??o.kind}],health:{state:'unknown'}}))}
export function exerciseReducer(topic:Topic,state:ExerciseState,action:ExerciseAction):ExerciseState {
 if(action.type==='reset')return {...initialExercise,generation:state.generation+1}
 const current=exercise(topic,state)
 if(action.token!==current.token||state.complete)return state
 const seen=(id:string,view:View)=>[...new Set([...state.seen,`${id}:${view}`])]
 switch(action.type){
  case 'select':return current.objects.some(o=>o.id===action.id)?{...state,selected:action.id,seen:seen(action.id,state.view)}:state
  case 'view':return state.selected?{...state,view:action.view,seen:seen(state.selected,action.view)}:state
  case 'hint':return current.check||state.fixed?state:{...state,hint:true}
  case 'diagnosis':return !state.fixed&&current.diagnoses.includes(action.value as Diagnosis)?{...state,diagnosis:action.value,result:null}:state
  case 'repair':return !state.fixed&&current.repairs.some(r=>r.id===action.value)?{...state,repair:action.value,result:null}:state
  case 'apply':{
   if(state.fixed)return state
   if(!current.required.every(key=>state.seen.includes(key)))return {...state,result:'evidence'}
   if(state.diagnosis!==current.caseId)return {...state,result:'diagnosis'}
   if(state.repair!==current.correctRepair)return {...state,result:'repair'}
   return {...state,fixed:true,result:null,hint:false,selected:null,seen:[]}
  }
  case 'next':return !state.fixed?state:!state.seen.includes(current.resultEvidence)?{...state,result:'observe'}:state.step===3?{...state,complete:true,result:null}:{...initialExercise,generation:state.generation,step:state.step+1}
 }
}
