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
