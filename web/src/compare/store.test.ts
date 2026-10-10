import {beforeEach,expect,it} from 'vitest'
import type {Target,TargetsView} from '../api/types'
import {clearComparison,compareWith,comparisonSelection,pinComparison,sameResource,selectionAvailable,useComparison} from './store'

const target:Target={provider:'kubernetes',id:'prod',title:'Production',configRev:'config-1',connection:{id:12,state:'connected',phase:'ready',attempt:1,maxAttempts:3,startedAt:1,attemptStarted:1}}
const ref={provider:'kubernetes',target:'prod',kind:'apps/deployments',scope:'ns',name:'web',uid:'u'}
const view:TargetsView={groups:[{provider:'kubernetes',title:'Kubernetes',targets:[target],problems:[]}],selected:null}
beforeEach(clearComparison)
it('pins only references, requires an admitted connection and invalidates changed configurations or incarnations',()=>{
 const selection=comparisonSelection(ref,target)!
 expect(selectionAvailable(selection,view)).toBe(true)
 expect(comparisonSelection({...ref,uid:''},target)).toBeNull()
 expect(comparisonSelection(ref,{...target,connection:undefined})).toBeNull()
 expect(selectionAvailable(selection,{...view,groups:[{...view.groups[0],targets:[{...target,configRev:'config-2'}]}]})).toBe(false)
 expect(selectionAvailable({...selection,connectionId:13},view)).toBe(false)
 pinComparison(selection);compareWith({...selection,ref:{...ref,uid:'v'}})
 expect(useComparison.getState().other?.ref.uid).toBe('v')
 expect(useComparison.getState().baseline).not.toHaveProperty('yaml')
 pinComparison(selection);expect(useComparison.getState().other).toBeNull()
 clearComparison();expect(useComparison.getState().baseline).toBeNull()
})
it('resource identity includes target, namespace, kind and UID, but not display title',()=>{
 expect(sameResource(ref,{...ref,title:'alias'})).toBe(true)
 for(const patch of [{uid:'v'},{target:'staging'},{scope:'other'},{kind:'pods'},{name:'other'},{provider:'compose'}])expect(sameResource(ref,{...ref,...patch})).toBe(false)
})
