import {ActionIconButton} from '../components/ActionIconButton'
import {ActionIcon} from '../components/icons'
import type {Ref} from '../api/types'
import {t} from '../i18n'
import {useStore} from '../store'
import {compareWith,comparisonSelection,pinComparison,sameResource,selectionAvailable,useComparison} from './store'

export function ComparisonTools({subject}:{subject:Ref}){
 const view=useStore(s=>s.view)
 const baseline=useComparison(s=>s.baseline)
 const target=view?.groups.flatMap(g=>g.targets).find(t=>t.provider===subject.provider&&t.id===subject.target)
 const selection=comparisonSelection(subject,target)
 return <><ActionIconButton label={t('compare.pin')} description={t('compare.pinHint')} disabled={!selection} onClick={()=>{if(selection)pinComparison(selection)}}><ActionIcon name="baseline"/></ActionIconButton>{<ActionIconButton label={t('compare.with')} description={t('compare.withHint')} disabled={!baseline||!selection||!selectionAvailable(baseline,view)||sameResource(subject,baseline.ref)} onClick={()=>{if(selection&&baseline)compareWith(selection)}}><ActionIcon name="compare"/></ActionIconButton>}</>
}
