import {it,expect,vi} from 'vitest'
import {render,screen,waitFor,cleanup} from '@testing-library/react'
import {LearningYaml} from './LearningYaml'
vi.mock('../components/YamlView',()=>{throw new Error('owned fixture: editor asset unavailable')})
it('retains local evidence and the surrounding lesson when the optional editor cannot load',async()=>{
 const caught=vi.spyOn(console,'error').mockImplementation(()=>{})
 try{
  const {rerender}=render(<><h1>Local lesson</h1><LearningYaml text="kind: Service" label="Evidence YAML" documentId="first"/></>)
  await waitFor(()=>expect(caught).toHaveBeenCalled())
  expect(screen.getByRole('heading',{name:'Local lesson'})).toBeInTheDocument()
  expect(screen.getByLabelText('Evidence YAML')).toHaveTextContent('kind: Service')
  rerender(<><h1>Local lesson</h1><LearningYaml text="kind: Pod" label="Evidence YAML" documentId="second"/></>)
  expect(screen.getByLabelText('Evidence YAML')).toHaveTextContent('kind: Pod')
 }finally{cleanup();caught.mockRestore()}
})
