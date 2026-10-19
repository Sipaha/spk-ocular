import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it } from 'vitest'
import { BaselineControl } from './BaselineControl'
import { comparisonSelection, clearComparison, pinComparison, useComparison } from './store'
import { initialState, useStore } from '../store'
import { loadLanguage, setLanguage, t } from '../i18n'
import { languages } from '../languages'
import { ComparisonTools } from './ComparisonTools'
import { connectedClient, k8s } from '../test/fakeClient'
beforeEach(()=>{useStore.setState({...initialState});clearComparison()})
it('keeps one header control before and after pin, exposes metadata on demand and clears without reading resource YAML',async()=>{
 const f=connectedClient([k8s('dev',{configRev:'1'})]);await f.client.listTargets();useStore.setState({view:f.state.view})
 const target=f.state.view.groups[0].targets[0],ref={provider:'kubernetes',target:'dev',kind:'pods',name:'api',scope:'web',uid:'u'}
 const selection=comparisonSelection(ref,target)!
 expect(selection).not.toBeNull()
 const user=userEvent.setup();const {container}=render(<BaselineControl/>)
 const button=screen.getByRole('button',{name:'Comparison baseline'})
 expect(container.querySelector('[data-comparison-baseline]')).toBeNull()
 act(()=>pinComparison(selection));expect(screen.getByRole('button',{name:'Comparison baseline'})).toBe(button)
 expect(screen.queryByRole('dialog')).toBeNull()
 await user.click(button);expect(screen.getByRole('dialog')).toHaveTextContent('web/api')
 await user.keyboard('{Escape}');expect(screen.queryByRole('dialog')).toBeNull();expect(button).toHaveFocus()
 await user.click(button);await user.click(screen.getByRole('button',{name:'Clear baseline'}))
 expect(useComparison.getState().baseline).toBeNull();expect(screen.getByRole('button',{name:'Comparison baseline'})).toBe(button)
 expect(screen.queryByRole('dialog')).toBeNull();expect(f.client.getResource).not.toHaveBeenCalled()
})

it('empty guidance names the resource-properties actions in every language; local pin identifies its object',async()=>{
 const user=userEvent.setup()
 try {
  for(const language of languages){
   await loadLanguage(language);act(()=>setLanguage(language))
   const view=render(<BaselineControl/>)
   await user.click(screen.getByRole('button',{name:t('compare.baseline')}))
   const dialog=screen.getByRole('dialog',{name:t('compare.baseline')})
   expect(dialog).toHaveTextContent(t('compare.pin'))
   expect(dialog).toHaveTextContent(t('compare.with'))
   expect(dialog.textContent).not.toMatch(/\{(?:pin|compare)\}/)
   await user.keyboard('{Escape}');view.unmount()
  }
 } finally {setLanguage('en')}
 render(<ComparisonTools subject={{provider:'kubernetes',target:'dev',kind:'pods',scope:'web',name:'payments-api',uid:'u'}}/>)
 const button=screen.getByRole('button',{name:'Use as comparison baseline'})
 expect(button).toHaveAccessibleDescription('Use payments-api as the baseline for comparing saved resources.')
})
