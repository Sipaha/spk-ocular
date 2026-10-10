import type {Ref} from '../api/types'
import {t} from '../i18n'
import {useStore} from '../store'
import {compareWith,comparisonSelection,pinComparison,sameResource,selectionAvailable,useComparison} from './store'

export function ComparisonTools({subject}:{subject:Ref}){
 const view=useStore(s=>s.view)
 const baseline=useComparison(s=>s.baseline)
 const target=view?.groups.flatMap(g=>g.targets).find(t=>t.provider===subject.provider&&t.id===subject.target)
 const selection=comparisonSelection(subject,target)
 const button='rounded-md border border-line px-2 py-0.5 text-xs text-fg-muted hover:bg-hover hover:text-fg disabled:pointer-events-none disabled:opacity-50'
 return <><button className={button} disabled={!selection} onClick={()=>{if(selection)pinComparison(selection)}}>{t('compare.pin')}</button>{baseline&&<button className={button} disabled={!selection||!selectionAvailable(baseline,view)||sameResource(subject,baseline.ref)} onClick={()=>{if(selection)compareWith(selection)}}>{t('compare.with')}</button>}</>
}
