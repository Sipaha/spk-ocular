import type {Row} from '../api/types'
import type {ConceptId} from './catalog'
export interface LabObject {uid:string; name:string; kind:ConceptId; owner?:string}
export interface LessonState {
 phase:'observe'|'deleted'|'replaced'|'challenge'|'complete'
 generation:number
 selected:string|null
 deleted:string|null
 chain:string[]
 foundOwner:boolean
 hint:boolean
 answer:string
 result:'retry'|'findOwner'|null
}
export type LessonAction = {type:'select';uid:string}|{type:'owner'}|{type:'delete'}|{type:'reconcile'}|{type:'challenge'}|{type:'reset'}|{type:'hint'}|{type:'answer';answer:string}|{type:'verify'}
export const initialLesson:LessonState={phase:'observe',generation:0,selected:null,deleted:null,chain:[],foundOwner:false,hint:false,answer:'',result:null}
export function labObjects(state:LessonState):LabObject[] {
 const root=state.phase==='challenge'||state.phase==='complete'?'orders':'web'
 const suffix=`${state.generation}`
 const dep={uid:`deployment-${suffix}`,name:root,kind:'apps/deployments' as const}
 const rs={uid:`replicaset-${suffix}`,name:`${root}-7db9`,kind:'apps/replicasets' as const,owner:dep.uid}
 const pods=[{uid:`pod-a-${suffix}`,name:`${root}-7db9-a`,kind:'pods' as const,owner:rs.uid},{uid:`pod-b-${suffix}`,name:`${root}-7db9-b`,kind:'pods' as const,owner:rs.uid}]
 const visible=pods.filter(p=>p.uid!==state.deleted)
 if(state.phase==='replaced')visible.push({uid:`pod-new-${suffix}`,name:`${root}-7db9-c`,kind:'pods',owner:rs.uid})
 return [dep,rs,...visible]
}
export function lessonRows(state:LessonState):Row[] {return labObjects(state).map(obj=>({id:obj.uid,ref:{provider:'learning',target:'local-lesson',kind:obj.kind,scope:'lesson',name:obj.name,uid:obj.uid},cells:[{text:obj.name},{text:obj.kind==='pods'?'Pod':obj.kind==='apps/replicasets'?'ReplicaSet':'Deployment'}],health:{state:'ok'}}))}
export function lessonReducer(state:LessonState,action:LessonAction):LessonState {
 const selected=labObjects(state).find(o=>o.uid===state.selected)
 switch(action.type){
  case 'reset':return {...initialLesson,generation:state.generation+1}
  case 'select':return labObjects(state).some(o=>o.uid===action.uid)?{...state,selected:action.uid,chain:[action.uid],result:null}:state
  case 'owner':{
   const owner=labObjects(state).find(o=>o.uid===selected?.owner)
   if(!owner)return state
   const chain=[...state.chain,owner.uid]
   const objects=labObjects(state)
   const valid=chain.length===3&&objects.find(o=>o.uid===chain[0])?.kind==='pods'&&objects.find(o=>o.uid===chain[1])?.kind==='apps/replicasets'&&owner.kind==='apps/deployments'
   return {...state,selected:owner.uid,chain,foundOwner:state.foundOwner||valid,result:null}
  }
  case 'delete':return state.phase==='observe'&&selected?.kind==='pods'?{...state,phase:'deleted',deleted:selected.uid,selected:null,chain:[]}:state
  case 'reconcile':return state.phase==='deleted'?{...state,phase:'replaced'}:state
  case 'challenge':return state.phase==='replaced'?{...initialLesson,phase:'challenge',generation:state.generation+1}:state
  case 'hint':return state.phase==='observe'||state.phase==='deleted'||state.phase==='replaced'?{...state,hint:true}:state
  case 'answer':return state.phase==='challenge'?{...state,answer:action.answer,result:null}:state
  case 'verify':return state.phase!=='challenge'?state:!state.foundOwner?{...state,result:'findOwner'}:state.answer!=='controller'?{...state,result:'retry'}:{...state,phase:'complete',result:null}
 }
}
export function labYAML(object:LabObject,objects:LabObject[]) {
 const owner=objects.find(o=>o.uid===object.owner)
 const kind=object.kind==='pods'?'Pod':object.kind==='apps/replicasets'?'ReplicaSet':'Deployment'
 return `apiVersion: ${object.kind==='pods'?'v1':'apps/v1'}\nkind: ${kind}\nmetadata:\n  name: ${object.name}\n  namespace: lesson\n  uid: ${object.uid}\n  labels:\n    app: ${object.name.startsWith('orders')?'orders':'web'}\n${owner?`  ownerReferences:\n    - kind: ${owner.kind==='apps/replicasets'?'ReplicaSet':'Deployment'}\n      name: ${owner.name}\n      uid: ${owner.uid}\n      controller: true\n`:''}spec:\n${object.kind==='pods'?'  containers:\n    - name: app\n      image: example.invalid/learning-app:v1\n':'  replicas: 2\n  selector:\n    matchLabels:\n      app: '+(object.name.startsWith('orders')?'orders':'web')+'\n  template:\n    metadata:\n      labels:\n        app: '+(object.name.startsWith('orders')?'orders':'web')+'\n    spec:\n      containers:\n        - name: app\n          image: example.invalid/learning-app:v1\n'}`
}
