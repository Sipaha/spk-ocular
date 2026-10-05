import http from 'node:http';
import path from 'node:path';
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
export function createSiteServer() {
 return http.createServer((req,res)=>{
  try {
   const pathname=decodeURIComponent(new URL(req.url,'http://localhost').pathname);
   if(!pathname.startsWith('/spk-ocular/')) {res.writeHead(404).end();return;}
   const rel=pathname.slice('/spk-ocular/'.length);
   const file=path.resolve('dist',rel.endsWith('/')?rel+'index.html':rel||'index.html');
   if(!file.startsWith(path.resolve('dist')+path.sep)){res.writeHead(403).end();return;}
   const content=readFileSync(file);
   const mime={'.html':'text/html','.css':'text/css','.js':'text/javascript','.mjs':'text/javascript','.svg':'image/svg+xml','.webp':'image/webp','.png':'image/png','.woff2':'font/woff2'}[path.extname(file)]||'application/octet-stream';
   res.writeHead(200,{'Content-Type':mime});res.end(content);
  }catch{res.writeHead(404).end();}
 });
}
if(process.argv[1] && import.meta.url===pathToFileURL(process.argv[1]).href){
 const server=createSiteServer();
 server.listen(Number(process.env.PORT||0),'127.0.0.1',()=>console.log(`http://127.0.0.1:${server.address().port}/spk-ocular/`));
 process.on('SIGTERM',()=>{server.closeAllConnections();server.close();});
}
