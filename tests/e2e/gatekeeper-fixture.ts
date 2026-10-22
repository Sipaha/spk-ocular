import {createServer} from 'node:http'
export async function gatekeeperServer(){
 const requests:{method:string;path:string;query:Record<string,string>;received:number;cancelled?:number}[]=[]
 const metadata=(name:string,uid:string,namespace='')=>({name,uid,namespace,resourceVersion:'7',creationTimestamp:'2026-10-01T00:00:00Z'})
 const violation={group:'',version:'v1',kind:'Secret',namespace:'other',name:'descriptive-secret',message:'Literal <script>not executed</script> https://example.invalid/not-opened',enforcementAction:'scoped',enforcementActions:['warn','dryrun'],uid:'UNVERIFIED-SUBJECT-UID',severity:'PRIVATE-SEVERITY',details:'PRIVATE-DETAILS'}
 const constraints=['required-labels','reported-zero','missing-audit','inconsistent','malformed','large-projection'].map(name=>({apiVersion:'constraints.gatekeeper.sh/v1beta1',kind:'K8sRequiredLabels',metadata:metadata(name,'constraint-'+name),spec:{enforcementAction:'scoped',parameters:{secret:'PRIVATE-PARAMETER'}},status:name==='missing-audit'?{}:{auditTimestamp:name==='malformed'?'invalid':'2026-10-21T00:00:00Z',totalViolations:name==='reported-zero'?0:name==='inconsistent'?0:5,violations:name==='reported-zero'?[]:name==='large-projection'?Array.from({length:140},()=>({...violation,message:'界'.repeat(600)})):[violation]}}))
 let denied=false,held=false,changing=false,replacing=false,selectedReads=0
 const cancellations:number[]=[]
 const server=createServer((req,res)=>{
  const u=new URL(req.url??'/','http://127.0.0.1'),path=u.pathname;const entry={method:req.method??'',path,query:Object.fromEntries(u.searchParams),received:performance.now(),cancelled:undefined as number|undefined};requests.push(entry)
  const send=(data:unknown,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(data))}
  const fail=(code:number)=>send({apiVersion:'v1',kind:'Status',status:'Failure',code,reason:code===403?'Forbidden':code===404?'NotFound':'MethodNotAllowed',message:'PRIVATE-SERVER'},code)
  if(req.method!=='GET')return fail(405)
  if(path==='/version')return send({gitVersion:'v1.34.0',major:'1',minor:'34'})
  if(path==='/api')return send({apiVersion:'v1',kind:'APIVersions',versions:['v1']})
  if(path==='/apis')return send({apiVersion:'v1',kind:'APIGroupList',groups:[{name:'constraints.gatekeeper.sh',versions:[{groupVersion:'constraints.gatekeeper.sh/v1beta1',version:'v1beta1'}],preferredVersion:{groupVersion:'constraints.gatekeeper.sh/v1beta1',version:'v1beta1'}}]})
  const resource=(name:string,kind:string,namespaced=false)=>({name,kind,namespaced,singularName:kind.toLowerCase(),verbs:['get','list','watch']})
  if(path==='/api/v1')return send({apiVersion:'v1',kind:'APIResourceList',groupVersion:'v1',resources:[resource('pods','Pod',true),resource('events','Event',true),resource('namespaces','Namespace')]})
  if(path==='/apis/constraints.gatekeeper.sh/v1beta1')return send({apiVersion:'v1',kind:'APIResourceList',groupVersion:'constraints.gatekeeper.sh/v1beta1',resources:[resource('k8srequiredlabels','K8sRequiredLabels')]})
  const match=path.match(/^\/(api\/v1|apis\/constraints.gatekeeper.sh\/v1beta1)\/(?:namespaces\/([^/]+)\/)?(pods|events|namespaces|k8srequiredlabels)(?:\/([^/]+))?$/)
  if(!match)return fail(404)
  const [,api,,resourceName,name]=match,kind=({pods:'Pod',events:'Event',namespaces:'Namespace',k8srequiredlabels:'K8sRequiredLabels'} as Record<string,string>)[resourceName]
  let rows:Record<string,unknown>[]=resourceName==='k8srequiredlabels'?constraints:resourceName==='namespaces'?[{apiVersion:'v1',kind:'Namespace',metadata:metadata('blue','ns-blue')}]:[]
  if(name){
   const row=rows.find(r=>(r.metadata as {name:string}).name===name);if(!row)return fail(404)
   if(resourceName==='k8srequiredlabels'){
    selectedReads++;if(denied)return fail(403)
    if(held){res.on('close',()=>{entry.cancelled=performance.now();cancellations.push(entry.cancelled-entry.received)});return}
    const result=structuredClone(row) as typeof constraints[number];if(changing&&selectedReads===2)result.metadata.resourceVersion='8';if(replacing&&selectedReads===2)result.metadata.uid='new-uid'
    return send(result)
   }
   return send(row)
  }
  const selector=u.searchParams.get('fieldSelector');if(selector?.startsWith('metadata.name='))rows=rows.filter(r=>(r.metadata as {name:string}).name===selector.slice(14))
  if(u.searchParams.get('watch')==='true'){
   res.writeHead(200,{'Content-Type':'application/json'})
   if(u.searchParams.get('sendInitialEvents')==='true'){for(const row of rows)res.write(JSON.stringify({type:'ADDED',object:row})+'\n');res.write(JSON.stringify({type:'BOOKMARK',object:{apiVersion:api==='api/v1'?'v1':api.slice(5),kind,metadata:{resourceVersion:'7',annotations:{'k8s.io/initial-events-end':'true'}}}})+'\n')}else res.write('\n')
   return
  }
  return send({apiVersion:api==='api/v1'?'v1':api.slice(5),kind:kind+'List',metadata:{resourceVersion:'7'},items:rows})
 })
 await new Promise<void>(r=>server.listen(0,'127.0.0.1',r))
 return {port:(server.address() as {port:number}).port,requests,cancellations,deny:(v:boolean)=>{denied=v},hold:(v:boolean)=>{held=v},change:(v:boolean)=>{changing=v;selectedReads=0},replace:(v:boolean)=>{replacing=v;selectedReads=0},close:async()=>{server.closeAllConnections();await new Promise<void>(r=>server.close(()=>r()))}}
}
