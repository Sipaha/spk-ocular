// YAML viewer and editor (CodeMirror 6). Loaded lazily: CodeMirror must
// never be in the entry chunk (scripts/check-bundle.mjs).
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands'
import { yaml } from '@codemirror/lang-yaml'
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { highlightSelectionMatches, search, searchKeymap } from '@codemirror/search'
import { EditorState } from '@codemirror/state'
import { EditorView, highlightActiveLine, keymap, lineNumbers } from '@codemirror/view'
import { tags } from '@lezer/highlight'
import { useEffect, useRef } from 'react'

const theme = EditorView.theme(
  {
    '&': { height: '100%', fontSize: '13px', backgroundColor: 'var(--color-app)', color: 'var(--color-fg)' },
    '.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.5' },
    '.cm-gutters': { backgroundColor: 'var(--color-sidebar)', color: 'var(--color-fg-subtle)', border: 'none' },
    '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--color-hover) 70%, transparent)' },
    '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': { backgroundColor: 'color-mix(in srgb, var(--color-accent) 30%, transparent) !important' },
    '.cm-panels': { backgroundColor: 'var(--color-panel)', color: 'var(--color-fg)' },
    '.cm-searchMatch': { backgroundColor: 'color-mix(in srgb, var(--color-warning) 35%, transparent)' },
  },
  { dark: true },
)

const highlight = HighlightStyle.define([
  { tag: tags.propertyName, color: '#9ecbff' },
  { tag: [tags.string, tags.special(tags.string)], color: '#a8d18d' },
  { tag: [tags.number, tags.bool, tags.null], color: '#e0b060' },
  { tag: tags.comment, color: 'var(--color-fg-subtle)', fontStyle: 'italic' },
  { tag: [tags.meta, tags.punctuation], color: 'var(--color-fg-muted)' },
])

interface Props {
  text: string
  /** Editable (fixed for the view's life: remount to switch); text is then
   * only the start — onChange gets every edit. */
  editable?: boolean
  onChange?: (text: string) => void
  /** Ctrl+Enter (⌘+Enter) in the editor. */
  onSubmit?: () => void
  autoFocus?: boolean
  label?: string
}

export default function YamlView({ text, editable, onChange, onSubmit, autoFocus, label }: Props) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const calls = useRef({ onChange, onSubmit })
  useEffect(() => {
    calls.current = { onChange, onSubmit }
  })
  useEffect(() => {
    view.current = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: text,
        extensions: [
          editable
            ? [
                history(),
                // Before the default keymap: Mod-Enter there inserts a line.
                keymap.of([{ key: 'Mod-Enter', run: () => (calls.current.onSubmit?.(), true) }, ...historyKeymap]),
                EditorView.updateListener.of((u) => {
                  if (u.docChanged) calls.current.onChange?.(u.state.doc.toString())
                }),
              ]
            : EditorState.readOnly.of(true),
          lineNumbers(),
          highlightActiveLine(),
          yaml(),
          syntaxHighlighting(highlight),
          search({ top: true }),
          highlightSelectionMatches(),
          keymap.of([...searchKeymap, ...defaultKeymap]),
          theme,
        ],
      }),
    })
    if (autoFocus) view.current.focus()
    return () => view.current?.destroy()
    // text updates are applied below without recreating the editor
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  useEffect(() => {
    const v = view.current
    // An editor's text is its own after the start (a late echo of onChange
    // must never overwrite what was typed meanwhile).
    if (v && !editable && v.state.doc.toString() !== text) {
      v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: text } })
    }
  }, [text, editable])
  return <div ref={host} className="h-full min-h-0" aria-label={label ?? 'yaml'} />
}
