import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { PanelResize, resizeFocusedPanel } from './PanelResize'
import { setLayoutResizing } from './layoutResize'

afterEach(() => { cleanup(); vi.restoreAllMocks(); setLayoutResizing(false) })

function fixture({ reverse = false, modal = false } = {}) {
  const done = vi.fn()
  render(<div style={{height:300}} data-testid="dock">
    <PanelResize label="dock" axis="height" reverse value={300} min={100} max={() => 600} onDone={done}/>
    <div role={modal ? 'dialog' : undefined} aria-modal={modal || undefined} style={{width:200}} data-testid="side">
      <PanelResize label="side" reverse={reverse} value={200} min={140} max={() => 240} onDone={done}/>
      <input aria-label="editor" defaultValue="keep my text"/>
    </div>
  </div>)
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function(this:HTMLElement) { return {width:parseFloat(this.style.width)||0,height:parseFloat(this.style.height)||0} as DOMRect })
  const input=screen.getByRole('textbox')
  input.focus()
  const key=(key:string, modifiers:KeyboardEventInit={}) => {
    let handled=false
    const listener=(event:KeyboardEvent)=>{handled=resizeFocusedPanel(event)}
    input.addEventListener('keydown',listener)
    fireEvent.keyDown(input,{key,altKey:true,shiftKey:true,...modifiers})
    input.removeEventListener('keydown',listener)
    return handled
  }
  return {key,done,input}
}

it('resizes the nearest matching boundary and keeps text and focus', () => {
  const {key,input,done}=fixture()
  expect(key('ArrowRight')).toBe(true)
  expect(screen.getByTestId('side').style.width).toBe('220px')
  expect(key('ArrowUp')).toBe(true)
  expect(screen.getByTestId('dock').style.height).toBe('320px')
  expect(done.mock.calls).toEqual([[220],[320]])
  expect(document.activeElement).toBe(input)
  expect(input).toHaveValue('keep my text')
})

it('honors reverse boundaries, limits and repeated keys', () => {
  const {key}=fixture({reverse:true})
  key('ArrowLeft');expect(screen.getByTestId('side').style.width).toBe('220px')
  for(let i=0;i<10;i++)key('ArrowLeft',{repeat:true})
  expect(screen.getByTestId('side').style.width).toBe('240px')
  for(let i=0;i<10;i++)key('ArrowRight',{repeat:true})
  expect(screen.getByTestId('side').style.width).toBe('140px')
})

it('does not steal other combinations, composition or active pointer resizing', () => {
  const {key,done}=fixture()
  for(const modifiers of [{altKey:false},{shiftKey:false},{ctrlKey:true},{metaKey:true},{isComposing:true}])expect(key('ArrowRight',modifiers)).toBe(false)
  setLayoutResizing(true)
  expect(key('ArrowRight')).toBe(false)
  expect(done).not.toHaveBeenCalled()
})

it('keeps modal resizing inside its own boundary', () => {
  const {key}=fixture({modal:true})
  expect(key('ArrowRight')).toBe(true)
  expect(key('ArrowUp')).toBe(false)
  expect(screen.getByTestId('dock').style.height).toBe('300px')
})

it('does not resize behind a modal or after unmount', () => {
  const {key,done}=fixture()
  const dialog=document.createElement('div');dialog.setAttribute('aria-modal','true');document.body.append(dialog)
  expect(key('ArrowRight')).toBe(false)
  dialog.remove();cleanup()
  expect(key('ArrowRight')).toBe(false)
  expect(done).not.toHaveBeenCalled()
})
