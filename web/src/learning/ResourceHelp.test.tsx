import {afterEach,beforeEach,it,expect} from 'vitest'
import {act,cleanup,fireEvent,render,screen} from '@testing-library/react'
import {setLanguage} from '../i18n'
import {ResourceExplanation,ResourceHelpButton,LearningDialog} from './ResourceHelp'
import {useLearning} from './store'
import {useStore} from '../store'
import {learn} from './content'
beforeEach(()=>{setLanguage('en');useLearning.getState().close()})
afterEach(()=>{cleanup();useLearning.getState().close();setLanguage('en')})
it('offers help only for Kubernetes and never invents help for custom kinds',()=>{
 const {rerender}=render(<ResourceHelpButton provider="compose" kind="pods"/>);expect(screen.queryByRole('button')).toBeNull()
 rerender(<ResourceHelpButton provider="kubernetes" kind="example.com/objects"/>);fireEvent.click(screen.getByRole('button'))
 expect(useLearning.getState().request?.kind).toBe('example.com/objects')
 render(<ResourceExplanation kind="example.com/objects" onConcept={()=>{throw Error('must not navigate')}}/>);expect(screen.getByText(learn('fallback'))).toBeInTheDocument()
})
it('navigates concepts locally, keeps exact field paths, and uses an explicit official link',()=>{
 let picked='';render(<ResourceExplanation kind="apps/replicasets" onConcept={id=>{picked=id}}/>);
 fireEvent.click(screen.getByRole('button',{name:'Deployment'}));expect(picked).toBe('apps/deployments')
 fireEvent.click(screen.getByText(learn('fields')));expect(screen.getByText('metadata.ownerReferences')).toBeInTheDocument()
 expect(screen.getByRole('link').getAttribute('href')).toBe('https://kubernetes.io/docs/concepts/workloads/controllers/replicaset/')
})
it('keeps lesson state while reading concepts and changing the application language',()=>{
 useLearning.getState().open();render(<LearningDialog languageMenu={null}/>);
 fireEvent.click(screen.getByRole('button',{name:learn('lesson')}));
 fireEvent.keyDown(document.querySelector('[data-lesson] [data-table-scroll]')!,{key:'End'});fireEvent.click(screen.getByRole('button',{name:learn('remove')}));
 expect(document.querySelector('[data-lesson]')).toHaveAttribute('data-phase','deleted')
 fireEvent.click(screen.getByRole('button',{name:learn('concepts')}));fireEvent.click(screen.getByRole('button',{name:learn('lesson')}));
 expect(document.querySelector('[data-lesson]')).toHaveAttribute('data-phase','deleted')
 act(()=>{setLanguage('ru');useStore.setState({info:{...useStore.getState().info,language:'ru'} as NonNullable<ReturnType<typeof useStore.getState>['info']>})})
 expect(document.querySelector('[data-lesson]')).toHaveAttribute('data-phase','deleted')
})

it('returns focus to the kind help button after closing the portal',()=>{
 render(<><ResourceHelpButton provider="kubernetes" kind="apps/deployments"/><LearningDialog languageMenu={null}/></>)
 const opener=screen.getByRole('button',{name:'What is this? Deployment'});fireEvent.click(opener)
 expect(screen.getByRole('dialog')).toHaveFocus();fireEvent.keyDown(screen.getByRole('dialog'),{key:'Escape'})
 expect(opener).toHaveFocus()
})

it('keeps keyboard ownership after a related concept replaces its trigger',()=>{
 useLearning.getState().open('apps/replicasets');render(<LearningDialog languageMenu={null}/>)
 fireEvent.click(screen.getByRole('navigation',{name:'Related concepts'}).querySelector('button')!)
 expect(screen.getByRole('dialog')).toHaveFocus();fireEvent.keyDown(screen.getByRole('dialog'),{key:'Escape'})
 expect(screen.queryByRole('dialog')).toBeNull()
})
