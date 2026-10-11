import {beforeEach,expect,it} from 'vitest'
import {dock,useDock} from '../dock/store'
import {usePanelWidths} from '../components/PanelResize'
import {fitsPins,parseWorkspace,serializeWorkspace} from './snapshot'
const target={provider:'kubernetes',id:'prod'}
const ref={provider:'kubernetes',target:'prod',kind:'pods',scope:'ns',name:'web',uid:'one',title:'display only'}
beforeEach(()=>useDock.setState({tabs:[],active:null,height:320}))
it('persists only deliberately pinned references and layout, never YAML, commands, terminal screens or display data',()=>{
 dock.openLogs(target,'secret display label',ref,'app');dock.pin(useDock.getState().active!)
 dock.openTerminal(target,'prod',{ref,command:['sh','-c','secret command'],attach:true,instance:'old-container',channel:'app'});dock.pin(useDock.getState().active!)
 expect(dock.openProperties(target,'prod',ref,'yaml')).toBe(true)
 const text=serializeWorkspace(useDock.getState(),usePanelWidths.getState())!
 for(const value of ['secret display label','secret command','display only','old-container','attach','command','instance','targetTitle','screen','yaml:'])expect(text).not.toContain(value)
 const snapshot=parseWorkspace(text)!
 expect(snapshot.panels.map(p=>p.kind)).toEqual(['logs','terminal','properties'])
 expect(snapshot.panels[0].channel).toBe('app')
 useDock.setState({tabs:[],active:null});dock.restore(snapshot)
 expect(useDock.getState().tabs.map(t=>t.kind)).toEqual(['logs','term-restored','properties'])
 expect(useDock.getState().tabs.every(t=>t.dormant&&t.pinned)).toBe(true)
})
it('bounds count and UTF-8 bytes; a rejected pin leaves existing panels and active selection unchanged',()=>{
 for(let i=0;i<8;i++)expect(dock.openProperties(target,'prod',{...ref,name:'p'+i,uid:'u'+i})).toBe(true)
 const before=useDock.getState()
 expect(dock.openProperties(target,'prod',{...ref,name:'ninth',uid:'ninth'})).toBe(false)
 expect(useDock.getState()).toBe(before)
 expect(fitsPins([...before.tabs,{...before.tabs[0],id:'big',ref:{...ref,name:'界'.repeat(1024)},pinned:true} as typeof before.tabs[0]])).toBe(false)
})
it('strictly validates stored state, rejects ambiguous identities and strips unrecognized fields',()=>{
 dock.openProperties(target,'prod',ref)
 const text=serializeWorkspace(useDock.getState(),usePanelWidths.getState())!,good=JSON.parse(text)
 for(const value of ['{',JSON.stringify({...good,v:2}),JSON.stringify({...good,panels:[{kind:'properties',ref:{...ref,uid:''}}]}),JSON.stringify({...good,panels:[good.panels[0],{...good.panels[0],tab:'yaml'}]}),JSON.stringify({...good,widths:{...good.widths,targets:'large'}}),JSON.stringify({...good,active:99})])expect(()=>parseWorkspace(value)).toThrow()
 const dirty={...good,panels:[{...good.panels[0],command:['SECRET'],yaml:'SECRET',ref:{...ref,title:'SECRET',credential:'SECRET'}}]}
 expect(JSON.stringify(parseWorkspace(JSON.stringify(dirty)))).not.toContain('SECRET')
 expect(parseWorkspace(undefined)).toBeNull()
})
it('several ended terminals for the same object remain separate and do not replay; live matching log tabs survive restore',()=>{
 dock.openTerminal(target,'prod',{ref});dock.pin(useDock.getState().active!)
 dock.openTerminal(target,'prod',{ref});dock.pin(useDock.getState().active!)
 dock.openLogs(target,'prod',ref);dock.pin(useDock.getState().active!)
 const snapshot=parseWorkspace(serializeWorkspace(useDock.getState(),usePanelWidths.getState())!)!
 useDock.setState({tabs:[],active:null});dock.openLogs(target,'prod',ref)
 const active=useDock.getState().active
 dock.restore(snapshot)
 expect(useDock.getState().active).toBe(active)
 expect(useDock.getState().tabs.filter(t=>t.kind==='term-restored')).toHaveLength(2)
 expect(useDock.getState().tabs[0]).toMatchObject({kind:'logs',pinned:true})
 expect(useDock.getState().tabs[0].dormant).not.toBe(true)
})
