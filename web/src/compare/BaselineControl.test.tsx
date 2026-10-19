import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it } from 'vitest'
import { BaselineControl } from './BaselineControl'
import { comparisonSelection, clearComparison, pinComparison, useComparison } from './store'
import { initialState, useStore } from '../store'
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
