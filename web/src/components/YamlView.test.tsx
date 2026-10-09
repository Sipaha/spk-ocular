import { fireEvent, render, waitFor } from '@testing-library/react'
import { EditorView } from '@codemirror/view'
import { describe, expect, it, vi } from 'vitest'
import YamlView from './YamlView'

const viewOf = (host: HTMLElement) => EditorView.findFromDOM(host.querySelector<HTMLElement>('.cm-editor')!)!

describe('YamlView', () => {
  it('is read-only unless editable', () => {
    const { container } = render(<YamlView text={'a: 1\n'} />)
    expect(viewOf(container).state.readOnly).toBe(true)
  })

  it('an editor reports every edit and Ctrl+Enter submits', async () => {
    const onChange = vi.fn()
    const onSubmit = vi.fn()
    const { container, rerender } = render(<YamlView text={'a: 1\n'} editable onChange={onChange} onSubmit={onSubmit} />)
    const v = viewOf(container)
    expect(v.state.readOnly).toBe(false)
    v.dispatch({ changes: { from: 3, to: 4, insert: '2' } })
    expect(onChange).toHaveBeenLastCalledWith('a: 2\n')
    // A late echo of an older text never overwrites what was typed.
    rerender(<YamlView text={'a: 1\n'} editable onChange={onChange} onSubmit={onSubmit} />)
    expect(v.state.doc.toString()).toBe('a: 2\n')
    fireEvent.keyDown(v.contentDOM, { key: 'Enter', code: 'Enter', ctrlKey: true })
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    expect(v.state.doc.toString()).toBe('a: 2\n') // no new line
  })
})

it('preserves CRLF when editing container text and loads filename syntax', async () => {
 const onChange=vi.fn()
 const {container}=render(<YamlView text={'const value = 1\r\n'} filename="config.js" editable onChange={onChange}/>)
 const v=viewOf(container)
 v.dispatch({changes:{from:14,to:15,insert:'2'}})
 expect(onChange).toHaveBeenLastCalledWith('const value = 2\r\n')
 await waitFor(()=>expect(container.querySelector('.cm-line span')).not.toBeNull())
})

it('uses physical shortcuts for history, select-all, search and comments', () => {
 const {container}=render(<YamlView text={'alpha: 1\nbeta: 2\n'} editable />)
 const v=viewOf(container)
 v.dispatch({changes:{from:7,to:8,insert:'9'}})
 fireEvent.keyDown(v.contentDOM,{key:'я',code:'KeyZ',ctrlKey:true})
 expect(v.state.doc.toString()).toBe('alpha: 1\nbeta: 2\n')
 fireEvent.keyDown(v.contentDOM,{key:'н',code:'KeyY',ctrlKey:true})
 expect(v.state.doc.toString()).toBe('alpha: 9\nbeta: 2\n')
 fireEvent.keyDown(v.contentDOM,{key:'ф',code:'KeyA',ctrlKey:true})
 expect(v.state.selection.main.to).toBe(v.state.doc.length)
 fireEvent.keyDown(v.contentDOM,{key:'.',code:'Slash',ctrlKey:true})
 expect(v.state.doc.toString()).toContain('# alpha')
 fireEvent.keyDown(v.contentDOM,{key:'а',code:'KeyF',ctrlKey:true})
 const input=container.querySelector<HTMLInputElement>('.cm-search input')!
 expect(input).not.toBeNull()
 fireEvent.keyDown(input,{key:'п',code:'KeyG',ctrlKey:true})
 fireEvent.keyDown(input,{key:'Escape',code:'Escape'})
 expect(container.querySelector('.cm-search')).toBeNull()
})

it('leaves unmodified letters and composition with text input', () => {
 const {container}=render(<YamlView text="original" editable />)
 const v=viewOf(container)
 v.dispatch({changes:{from:0,to:8,insert:'changed'}})
 fireEvent.keyDown(v.contentDOM,{key:'я',code:'KeyZ'})
 expect(v.state.doc.toString()).toBe('changed')
 fireEvent.keyDown(v.contentDOM,{key:'я',code:'KeyZ',ctrlKey:true,isComposing:true})
 expect(v.state.doc.toString()).toBe('changed')
})
