import {createServer, type IncomingMessage, type ServerResponse} from 'node:http'
import {createHash} from 'node:crypto'
import type {Socket} from 'node:net'

/** Disposable Kubernetes HTTP/WebSocket protocol fixture. Never executes argv. */
export async function nodeShellFixture() {
  type Object = Record<string, any> // Kubernetes JSON, intentionally schema-variable.
  const node:Object={apiVersion:'v1',kind:'Node',metadata:{name:'worker-01',uid:'node-1',resourceVersion:'1'},spec:{},status:{nodeInfo:{operatingSystem:'linux',architecture:'amd64',kubeletVersion:'v1.35.0'},conditions:[{type:'Ready',status:'True'}]}}
  const namespaces=['kube-system','tools','blocked'].map(name=>({apiVersion:'v1',kind:'Namespace',metadata:{name,uid:`ns-${name}`,resourceVersion:'1',labels:{'pod-security.kubernetes.io/enforce':name==='blocked'?'restricted':'privileged'}}}))
  const pods=new Map<string,Object>(), sockets=new Set<Socket>()
  const calls:{method:string;path:string;body?:Object}[]=[]
  const execSockets=new Set<import('node:stream').Duplex>()
  let execs=0, resizes=0, denyCreate=false, denyDelete=false
  const reply=(r:ServerResponse,d:unknown,status=200)=>{r.writeHead(status,{'Content-Type':'application/json'});r.end(JSON.stringify(d))}
  const status=(r:ServerResponse,reason:string,code:number)=>reply(r,{apiVersion:'v1',kind:'Status',status:'Failure',reason,message:reason,code},code)
  const server=createServer(async(req:IncomingMessage,res:ServerResponse)=>{
    const url=new URL(req.url!,'http://fixture'),path=url.pathname,method=req.method!
    let body:Object|undefined
    if(method==='POST'||method==='DELETE'){const chunks=[];for await(const chunk of req)chunks.push(chunk);body=JSON.parse(Buffer.concat(chunks).toString()||'{}')}
    calls.push({method,path,body})
    if(path==='/version')return reply(res,{gitVersion:'v1.35.0'})
    if(path==='/api')return reply(res,{kind:'APIVersions',versions:['v1']})
    if(path==='/apis')return reply(res,{kind:'APIGroupList',groups:[]})
    if(path.endsWith('/selfsubjectreviews'))return reply(res,{apiVersion:'authentication.k8s.io/v1',kind:'SelfSubjectReview',status:{userInfo:{username:'fixture-admin',groups:['system:authenticated']}}})
    if(path.endsWith('/selfsubjectaccessreviews'))return reply(res,{apiVersion:'authorization.k8s.io/v1',kind:'SelfSubjectAccessReview',status:{allowed:true}})
    if(path==='/api/v1')return reply(res,{kind:'APIResourceList',groupVersion:'v1',resources:[['nodes','Node',false],['namespaces','Namespace',false],['pods','Pod',true],['events','Event',true]].map(([name,kind,namespaced])=>({name,kind,namespaced,singularName:String(kind).toLowerCase(),verbs:['get','list','watch','create','delete']}))})
    if(url.searchParams.get('sendInitialEvents')==='true')return status(res,'BadRequest',400)
    if(url.searchParams.get('watch')==='true'){res.writeHead(200,{'Content-Type':'application/json'});res.flushHeaders();const timer=setInterval(()=>res.write('\n'),1000);res.on('close',()=>clearInterval(timer));return}
    const m=path.match(/^\/api\/v1\/(?:namespaces\/([^/]+)\/)?(nodes|namespaces|pods|events)(?:\/([^/]+))?$/)
    if(!m)return status(res,'NotFound',404)
    const [,ns,resource,name]=m
    if(method==='POST'&&resource==='pods'){
      if(denyCreate||ns==='blocked')return status(res,'Forbidden',403)
      if(pods.has(`${ns}/${body!.metadata.name}`))return status(res,'AlreadyExists',409)
      const p=structuredClone(body!);p.metadata.uid='helper-'+calls.length;p.metadata.resourceVersion='1';p.status={phase:'Running',containerStatuses:[{name:'shell',ready:true,state:{running:{}}}]};pods.set(`${ns}/${p.metadata.name}`,p);return reply(res,p,201)
    }
    if(method==='DELETE'&&resource==='pods'){
      if(denyDelete)return status(res,'Forbidden',403)
      const p=pods.get(`${ns}/${name}`);if(!p)return status(res,'NotFound',404)
      if(body?.preconditions?.uid!==p.metadata.uid)return status(res,'Conflict',409)
      pods.delete(`${ns}/${name}`);return reply(res,{kind:'Status',status:'Success'})
    }
    let items:Object[]=resource==='nodes'?[node]:resource==='namespaces'?namespaces:resource==='pods'?[...pods.values()].filter(p=>!ns||p.metadata.namespace===ns):[]
    if(name){const item=items.find(p=>p.metadata.name===name);return item?reply(res,item):status(res,'NotFound',404)}
    const selector=url.searchParams.get('fieldSelector');if(selector?.startsWith('metadata.name='))items=items.filter(p=>p.metadata.name===selector.slice(14))
    return reply(res,{apiVersion:'v1',kind:resource==='nodes'?'NodeList':resource==='namespaces'?'NamespaceList':resource==='pods'?'PodList':'EventList',metadata:{resourceVersion:'1'},items})
  })
  server.on('connection',s=>{sockets.add(s);s.on('close',()=>sockets.delete(s))})
  server.on('upgrade',(req,socket)=>{
    const url=new URL(req.url!,'http://fixture')
    if(!url.pathname.endsWith('/exec')){socket.destroy();return}
    execSockets.add(socket);socket.on('close',()=>execSockets.delete(socket))
    calls.push({method:'UPGRADE',path:url.pathname,body:{argv:url.searchParams.getAll('command')}});execs++
    const key=createHash('sha1').update(req.headers['sec-websocket-key']+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64')
    socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${key}\r\nSec-WebSocket-Protocol: v5.channel.k8s.io\r\n\r\n`)
    function frame(payload:Buffer,opcode=2){const n=payload.length;socket.write(Buffer.concat([n<126?Buffer.from([0x80|opcode,n]):Buffer.from([0x80|opcode,126,n>>8,n&255]),payload]))}
    frame(Buffer.concat([Buffer.from([1]),Buffer.from('fixture node worker-01 (root)\r\n# ')]))
    let input=Buffer.alloc(0),line=''
    socket.on('data',(chunk:Buffer)=>{
      input=Buffer.concat([input,chunk])
      while(input.length>=2){const opcode=input[0]&15,masked=!!(input[1]&128);let len=input[1]&127,off=2;if(len===126){if(input.length<4)return;len=input.readUInt16BE(2);off=4}if(len===127){socket.destroy();return}if(input.length<off+(masked?4:0)+len)return
        const mask=masked?input.subarray(off,off+4):null;if(mask)off+=4
        const payload=Buffer.from(input.subarray(off,off+len));input=input.subarray(off+len);if(mask)for(let i=0;i<len;i++)payload[i]^=mask[i%4]
        if(opcode===8){frame(Buffer.from([3,232]),8);socket.end();return}
        if(opcode===9){frame(payload,10);continue}
        if(opcode!==2)continue
        if(payload[0]===4){resizes++;continue}
        if(payload[0]!==0)continue
        for(const ch of payload.subarray(1).toString()){
          if(ch==='\r'||ch==='\n'){
            if(line==='exit'){frame(Buffer.concat([Buffer.from([3]),Buffer.from(JSON.stringify({status:'Success'}))]));frame(Buffer.from([3,232]),8);socket.end();return}
            frame(Buffer.concat([Buffer.from([1]),Buffer.from(`\r\nfixture echo: ${line}\r\n# `)]));line=''
          }else if(ch>=' ')line+=ch
        }
      }
    })
    socket.on('error',()=>{})
  })
  await new Promise<void>(r=>server.listen(0,'127.0.0.1',r))
  return {url:`http://127.0.0.1:${(server.address() as {port:number}).port}`,calls,pods,node,get execs(){return execs},get resizes(){return resizes},denyCreate(v:boolean){denyCreate=v},denyDelete(v:boolean){denyDelete=v},dropExecs(){for(const s of execSockets)s.destroy()},async close(){for(const s of sockets)s.destroy();await new Promise<void>(r=>server.close(()=>r()))}}
}
