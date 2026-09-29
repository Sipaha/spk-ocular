// Read-only YAML viewer (CodeMirror 6). Loaded lazily: CodeMirror must never
// be in the entry chunk (scripts/check-bundle.mjs).
import { defaultKeymap } from '@codemirror/commands'
import { yaml } from '@codemirror/lang-yaml'
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { highlightSelectionMatches, search, searchKeymap } from '@codemirror/search'
import { EditorState } from '@codemirror/state'
import { EditorView, highlightActiveLine, keymap, lineNumbers } from '@codemirror/view'
import { tags } from '@lezer/highlight'
import { useEffect, useRef } from 'react'

const theme = EditorView.theme(
  {
    '&': { height: '100%', fontSize: '12px', backgroundColor: 'var(--color-app)', color: 'var(--color-fg)' },
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

export default function YamlView({ text }: { text: string }) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  useEffect(() => {
    view.current = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: text,
        extensions: [
          EditorState.readOnly.of(true),
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
    return () => view.current?.destroy()
    // text updates are applied below without recreating the editor
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  useEffect(() => {
    const v = view.current
    if (v && v.state.doc.toString() !== text) {
      v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: text } })
    }
  }, [text])
  return <div ref={host} className="h-full min-h-0" aria-label="yaml" />
}
