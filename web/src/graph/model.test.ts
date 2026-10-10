import {describe,it,expect} from 'vitest'
import type {ClusterGraph,GraphNode} from '../api/types'
import {fitCamera,layoutGraph,zoomAt} from './model'
const node=(id:string,scope:string):GraphNode=>({id,ref:{provider:'test',target:'t',kind:'objects',name:id,scope,uid:id},kindTitle:'Object',health:'ok'})
const graph=(nodes:GraphNode[],edges:ClusterGraph['edges']=[]):ClusterGraph=>({nodes,edges,problems:[],truncated:false,discovery:'ready',capturedAt:0})
describe('cluster graph layout and camera',()=>{
 it('keeps namespaces separate, stable identities and cycles finite',()=>{
  const data=graph([node('a','blue'),node('b','green'),node('c','blue'),node('global','')],[{source:'a',target:'c',type:'owns'},{source:'c',target:'a',type:'uses'}])
  const layout=layoutGraph(data)
  expect(layout.regions.map(r=>r.name)).toEqual(['','blue','green'])
  expect(layout.nodes.map(n=>n.id).sort()).toEqual(['a','b','c','global'])
  expect(layout.nodes.every(n=>Number.isFinite(n.x)&&Number.isFinite(n.y))).toBe(true)
  expect(layoutGraph(data)).toEqual(layout)
  for(const n of layout.nodes){const region=layout.regions.find(r=>r.name===(n.ref.scope??''))!;expect(n.x).toBeGreaterThanOrEqual(region.x);expect(n.y).toBeGreaterThanOrEqual(region.y)}
 })
 it('anchors zoom at the pointer and bounds extreme input',()=>{
  const camera={x:30,y:20,zoom:.8},next=zoomAt(camera,2,200,100)
  expect((200-next.x)/next.zoom).toBeCloseTo((200-camera.x)/camera.zoom)
  expect((100-next.y)/next.zoom).toBeCloseTo((100-camera.y)/camera.zoom)
  expect(zoomAt(camera,Infinity,0,0).zoom).toBe(4)
  expect(zoomAt(camera,0,0,0).zoom).toBe(.002)
  expect(fitCamera(layoutGraph(graph([])),100,100).zoom).toBeGreaterThan(0)
 })
 it('lays out 30,000 resources without quadratic force iterations',()=>{
  const nodes=Array.from({length:30000},(_,i)=>node('resource-'+i,'namespace-'+i%30))
  const data=graph(nodes,nodes.slice(1).map((n,i)=>({source:nodes[i].id,target:n.id,type:'owns'})))
  const start=performance.now(),layout=layoutGraph(data)
  expect(layout.nodes).toHaveLength(30000)
  expect(layout.regions).toHaveLength(30)
  expect(performance.now()-start).toBeLessThan(1500)
 })
})
