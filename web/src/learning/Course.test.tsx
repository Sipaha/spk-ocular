import {afterEach,beforeEach,it,expect,vi} from 'vitest'
import {act,cleanup,fireEvent,render,screen} from '@testing-library/react'
import {Course} from './Course'
import {pathCopy,pathText} from './pathContent'
import {setLanguage} from '../i18n'
import {useStore} from '../store'
import {LearningDialog} from './ResourceHelp'
import {useLearning} from './store'
vi.mock('../components/YamlView',()=>({default:({text}:{text:string})=><pre>{text}</pre>}))
beforeEach(()=>{setLanguage('en');useLearning.getState().close()})
afterEach(()=>{cleanup();useLearning.getState().close();setLanguage('en')})
const openNetwork=()=>fireEvent.click(screen.getByRole('button',{name:`${pathText('open')}: ${pathCopy().topics.network}`}))
it('lists the introductory lesson and five practical modules with a truthful completion count',()=>{
 const intro=vi.fn();render(<Course introComplete onIntro={intro}/>);
 expect(screen.getByText('1 of 6 lessons complete')).toBeInTheDocument()
 expect(document.querySelectorAll('[data-course-topic]')).toHaveLength(5)
 fireEvent.click(screen.getAllByRole('button',{name:/Open lesson:/})[0]);expect(intro).toHaveBeenCalledOnce()
})
it('keeps per-module evidence and answers across overview navigation, but reset clears them',()=>{
 render(<Course introComplete={false} onIntro={()=>{}}/>);openNetwork()
 fireEvent.click(screen.getByRole('gridcell',{name:'web-1'}));fireEvent.click(screen.getByRole('tab',{name:'YAML'}))
 expect(document.querySelector('[data-evidence="service:yaml"]')).toHaveAttribute('data-inspected','true')
 fireEvent.click(screen.getByRole('radio',{name:pathCopy().diagnoses.selector}))
 fireEvent.click(screen.getByRole('button',{name:pathText('overview')}))
 fireEvent.click(screen.getByRole('button',{name:`${pathText('open')}: ${pathCopy().topics.runtime}`}))
 fireEvent.click(screen.getByRole('button',{name:pathText('overview')}));openNetwork()
 expect(screen.getByRole('radio',{name:pathCopy().diagnoses.selector})).toBeChecked()
 expect(document.querySelector('[data-evidence="service:yaml"]')).toHaveAttribute('data-inspected','true')
 fireEvent.click(screen.getByRole('button',{name:'Reset lesson'}))
 expect(document.querySelector('[data-evidence="service:yaml"]')).toHaveAttribute('data-inspected','false')
 expect(screen.getByRole('radio',{name:pathCopy().diagnoses.selector})).not.toBeChecked()
})
it('keeps course evidence during language/reference switches and clears it on library close',()=>{
 useLearning.getState().open();render(<LearningDialog languageMenu={null}/>);
 fireEvent.click(screen.getByRole('button',{name:pathText('course')}));openNetwork()
 fireEvent.click(screen.getByRole('gridcell',{name:'web-1'}));fireEvent.click(screen.getByRole('tab',{name:'YAML'}))
 fireEvent.click(screen.getByRole('button',{name:'Resource types'}));fireEvent.click(screen.getByRole('button',{name:pathText('course')}))
 act(()=>{setLanguage('ru');useStore.setState({info:{...useStore.getState().info,language:'ru'} as NonNullable<ReturnType<typeof useStore.getState>['info']>})})
 expect(document.querySelector('[data-evidence="service:yaml"]')).toHaveAttribute('data-inspected','true')
 expect(screen.getByRole('heading',{name:pathCopy().topics.network})).toBeInTheDocument()
 act(()=>useLearning.getState().close());act(()=>useLearning.getState().open())
 fireEvent.click(screen.getByRole('button',{name:pathText('course')}));openNetwork()
 expect(document.querySelector('[data-evidence="service:yaml"]')).toHaveAttribute('data-inspected','false')
})
