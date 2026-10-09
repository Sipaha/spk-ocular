import {readdirSync} from 'node:fs';
import {join,relative,extname} from 'node:path';
import {URL,fileURLToPath} from 'node:url';

// TS resolves extensionless imports case-insensitively on Windows/macOS.
// Treat .ts and .tsx with the same folded module path as a collision everywhere.
const root=fileURLToPath(new URL('../src/',import.meta.url));
const modules=new Map();
function visit(directory){
 for(const entry of readdirSync(directory,{withFileTypes:true})){
  const path=join(directory,entry.name);
  if(entry.isDirectory()){visit(path);continue;}
  const extension=extname(path);
  if(!['.ts','.tsx','.js','.jsx','.mts','.cts','.mjs','.cjs'].includes(extension))continue;
  const name=relative(root,path);
  const key=name.slice(0,-extension.length).toLocaleLowerCase('en-US');
  const previous=modules.get(key);
  if(previous)throw new Error(`Case-insensitive module collision: ${previous} and ${name}`);
  modules.set(key,name);
 }
}
visit(root);
console.log(`check-source-paths: ${modules.size} unique portable module paths`);
