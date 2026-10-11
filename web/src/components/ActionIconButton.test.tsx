import {act,fireEvent,render,screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {createRef} from 'react'
import {describe,expect,it,vi} from 'vitest'
import {ActionIconButton} from './ActionIconButton'
import {ActionIcon} from './icons'

describe('ActionIconButton',()=>{
 it('keeps a concise accessible name and description before and during a focus tooltip',async()=>{
  render(<ActionIconButton label="Logs" description="Open logs; right-click to choose a container."><ActionIcon name="logs"/></ActionIconButton>)
  const button=screen.getByRole('button',{name:'Logs'})
  expect(button).toHaveAccessibleDescription('Open logs; right-click to choose a container.')
  expect(button).toHaveTextContent('')
  await userEvent.hover(button)
  expect(screen.getByRole('tooltip')).toBeVisible()
  await userEvent.keyboard('{Escape}')
  expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  await userEvent.tab()
  expect(button).toHaveFocus()
  expect(screen.getByRole('tooltip')).toHaveTextContent('right-click')
  expect(button).toHaveAccessibleDescription('Open logs; right-click to choose a container.')
 })
 it('dismisses the tooltip with Escape without closing the containing panel; a later Escape reaches the panel',async()=>{
  const close=vi.fn()
  render(<section onKeyDown={e=>{if(e.key==='Escape')close()}}><ActionIconButton label="Edit" description="Edit saved YAML"><ActionIcon name="edit"/></ActionIconButton></section>)
  await userEvent.tab();await userEvent.keyboard('{Escape}')
  expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  expect(close).not.toHaveBeenCalled()
  await userEvent.keyboard('{Escape}')
  expect(close).toHaveBeenCalledOnce()
 })
 it('forwards button refs, click and context-menu setup; clicking clears the tooltip',async()=>{
  const click=vi.fn(),context=vi.fn(),ref=createRef<HTMLButtonElement>()
  render(<ActionIconButton ref={ref} label="Files" description="Right-click for setup" onClick={click} onContextMenu={context}><ActionIcon name="files"/></ActionIconButton>)
  const button=screen.getByRole('button',{name:'Files'})
  expect(ref.current).toBe(button)
  await userEvent.hover(button)
  expect(screen.getByRole('tooltip')).toBeVisible()
  await userEvent.click(button)
  expect(click).toHaveBeenCalledOnce()
  expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  fireEvent.contextMenu(button)
  expect(context).toHaveBeenCalledOnce()
 })
 it('shows only the most recently hovered or focused tooltip',async()=>{
  render(<><ActionIconButton label="Files" description="File setup"><ActionIcon name="files"/></ActionIconButton><ActionIconButton label="Copy" description="Copy visible logs"><ActionIcon name="copy"/></ActionIconButton></>)
  await userEvent.hover(screen.getByRole('button',{name:'Files'}))
  expect(screen.getByRole('tooltip')).toHaveTextContent('File setup')
  act(()=>screen.getByRole('button',{name:'Copy'}).focus())
  await screen.findByRole('tooltip',{name:'Copy visible logs'})
  expect(screen.getAllByRole('tooltip')).toHaveLength(1)
 })
 it('retains disabled and pressed semantics and removes portal content on unmount',async()=>{
  const click=vi.fn()
  const {unmount}=render(<ActionIconButton disabled aria-pressed label="Previous" description="Previous instance logs" onClick={click}><ActionIcon name="previous"/></ActionIconButton>)
  const button=screen.getByRole('button',{name:'Previous'})
  expect(button).toBeDisabled();expect(button).toHaveAttribute('aria-pressed','true')
  await userEvent.click(button);expect(click).not.toHaveBeenCalled()
  await userEvent.hover(button.parentElement!)
  expect(screen.getByRole('tooltip')).toBeVisible()
  unmount();expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
 })
})
