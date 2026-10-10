import {describe,it,expect} from 'vitest'
import type {ClusterTimeline,Ref,TimelineEvent,TimelineResource} from '../api/types'
import {timelineBounds,timelineDensity,timelineRows} from './model'
const ref=(uid:string,name=uid,scope='blue'):Ref=>({provider:'kubernetes',target:'t',kind:'pods',scope,name,uid})
const resource=(uid:string,ownerUid='',name=uid):TimelineResource=>({ref:ref(uid,name),kindTitle:'Pod',ownerUid})
const event=(id:string,uid:string,over:Partial<TimelineEvent>={}):TimelineEvent=>({id,ref:ref('event-'+id),subject:ref(uid),subjectKind:'Pod',openable:true,type:'Warning',reason:'BackOff',message:'failed',count:10,firstAt:100000,lastAt:160000,timeFallback:false,messageTruncated:false,...over})
const snapshot=(events:TimelineEvent[],resources:TimelineResource[]=[]):ClusterTimeline=>({events,resources,problems:[],discovery:'ready',truncated:false,capturedAt:180000})
const all={query:'',warnings:false,kind:'',rangeMs:0}
describe('Events timeline evidence model',()=>{
 it('groups the immutable ownership chain and filters by ancestor name',()=>{
  const data=snapshot([event('e','pod')],[resource('pod','rs'),resource('rs','deployment'),resource('deployment','','api')])
  const rows=timelineRows(data,{...all,query:'api'})
  expect(rows).toHaveLength(1)
  expect(rows[0].chain.map(r=>r.ref.uid)).toEqual(['deployment','rs','pod'])
  expect(rows[0].incomplete).toBe(false)
 })
 it('never associates historical same-name events with replacements or another namespace',()=>{
  const historical=event('e','old',{subject:ref('old','api')})
  const rows=timelineRows(snapshot([historical],[resource('new','replacement','api'),resource('replacement')]),all)
  expect(rows[0].root.ref.uid).toBe('old');expect(rows[0].incomplete).toBe(true)
  const cross=resource('parent');cross.ref.scope='green'
  const separate=timelineRows(snapshot([event('e','pod')],[resource('pod','parent'),cross]),all)
  expect(separate[0].chain).toHaveLength(1);expect(separate[0].incomplete).toBe(true)
 })
 it('bounds malformed cycles and ownership depth without recursion',()=>{
  const cycle=timelineRows(snapshot([event('e','a')],[resource('a','b'),resource('b','a')]),all)
  expect(cycle[0].chain).toHaveLength(2);expect(cycle[0].incomplete).toBe(true)
  const long=Array.from({length:100},(_,i)=>resource(String(i),String(i+1)))
  expect(timelineRows(snapshot([event('e','0')],long),all)[0].chain).toHaveLength(32)
 })
 it('filters warning, kind, text and time against the snapshot, without inflating occurrence counts',()=>{
  const data=snapshot([event('a','a'),event('b','b',{type:'Normal',reason:'Started',subjectKind:'Deployment',lastAt:179000}),event('c','c',{firstAt:0,lastAt:0})])
  expect(timelineRows(data,{...all,warnings:true,rangeMs:30000}).map(r=>r.event.id)).toEqual(['a'])
  expect(timelineRows(data,{...all,kind:'Deployment',query:'Started'}).map(r=>r.event.id)).toEqual(['b'])
  expect(timelineRows(data,all)).toHaveLength(3)
  const rows=timelineRows(data,{...all,rangeMs:30000})
  const {start,end}=timelineBounds(rows,data.capturedAt,30000)
  const bins=timelineDensity(rows,start,end)
  expect(bins.reduce((n,b)=>n+b.normal+b.warning,0)).toBe(2)
  expect(rows[0].event.count).toBe(10)
 })
 it('keeps unknown times and empty snapshots finite and includes a clock-skewed event',()=>{
  expect(timelineBounds([],0,0)).toEqual({start:-60000,end:0})
  const rows=timelineRows(snapshot([event('x','x',{firstAt:0,lastAt:0}),event('future','f',{firstAt:250000,lastAt:300000})]),all)
  const bounds=timelineBounds(rows,180000,0)
  expect(bounds.end).toBe(300000);expect(bounds.start).toBe(180000)
  expect(timelineDensity(rows,bounds.start,bounds.end).reduce((n,b)=>n+b.warning,0)).toBe(1)
 })
 it('processes 10,000 series with shared ownership without quadratic grouping',()=>{
  const events=Array.from({length:10000},(_,i)=>event(String(i),'pod'))
  const data=snapshot(events,[resource('pod','rs'),resource('rs','deployment'),resource('deployment')])
  const start=performance.now()
  expect(timelineRows(data,all)).toHaveLength(10000)
  expect(performance.now()-start).toBeLessThan(1000)
 })
})
