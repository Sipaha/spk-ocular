import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { ApiError, type Client } from '../api/client'
import type { Ref, Value, ValueKey, ValueList, ValueOp, ValueResult } from '../api/types'
import { classLabel, t } from '../i18n'
import { showNotice } from '../store'
import { copyValue } from './copy'
import { ValueDialog, type ValueDialogMode } from './ValueDialog'

interface Props {
  client: Client
  /** The object shown, with its provider, target and UID. */
  subject: Ref
  /** The object's revision now (the details' live view): a change hides what is shown. */
  revision?: string
  /** The kind's name for one object (the review names it). */
  kindTitle: string
}

/** A shown value is cut here; the rest on request. */
const SHOWN_CHARS = 64 << 10

const toolBtn = 'rounded-md border border-line px-1.5 py-0.5 text-xs text-fg-muted hover:bg-hover hover:text-fg disabled:pointer-events-none disabled:opacity-50'
const codeOf = (e: unknown) => (e instanceof ApiError ? e.code : 'internal')
const detailOf = (e: unknown) => (e instanceof ApiError ? e.detail || e.code : e instanceof Error ? e.message : String(e))

/** A key's line: its value shown (of the listed version), being read, or why not. */
type Line = { state: 'reading' } | { state: 'shown'; v: Value; whole: boolean } | { state: 'changed' } | { state: 'error'; text: string }

/**
 * An object's protected values: keys with sizes; a value is read only on
 * an explicit action on one key (show, copy) and lives only in this
 * section's state — hidden when the details close, move to another object,
 * tab, view or target (the section unmounts), when the object's revision
 * changes, and by Hide. A late answer is dropped: by generation (per key and
 * for the section), liveness, and its origin (UID and version asked about).
 */
export function ValuesSection({ client, subject, revision, kindTitle }: Props) {
  const [list, setList] = useState<{ data?: ValueList; error?: string } | null>(null)
  const [lines, setLines] = useState<Record<string, Line>>({})
  const [dialog, setDialog] = useState<ValueDialogMode | null>(null)
  const [reload, setReload] = useState(0)
  const live = useRef(true)
  // Bumped by Hide all and by a new list: every answer asked before is stale.
  const gen = useRef(0)
  const keyGen = useRef(new Map<string, number>())
  // The listed version now (callbacks check an answer against it).
  const listed = useRef<string | undefined>(undefined)
  const uid = subject.uid ?? ''

  useLayoutEffect(() => {
    live.current = true
    return () => {
      live.current = false
    }
  }, [])

  // Keys are read on opening, again when the object's revision moves away
  // from the listed one (each new revision once), and after a write.
  const behind = list?.data && revision && revision !== list.data.version ? revision : null
  const fetched = useRef(-1)
  useEffect(() => {
    if (behind === null && fetched.current === reload) return
    fetched.current = reload
    let current = true
    client.getValues(subject).then(
      (data) => {
        if (!current || !live.current) return
        gen.current++
        listed.current = data.version
        setList({ data })
        // What was shown is of another version: "changed", never kept.
        setLines((ls) => {
          const out: Record<string, Line> = {}
          for (const [k, l] of Object.entries(ls)) {
            if (l.state === 'error') continue
            out[k] = l.state === 'shown' && l.v.version === data.version ? l : { state: 'changed' }
          }
          return out
        })
      },
      (e) => current && live.current && setList({ error: t('values.loadFailed', { class: classLabel(codeOf(e)), detail: detailOf(e) }) }),
    )
    return () => {
      current = false
    }
    // subject: the section is keyed by the object.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, behind, reload])

  const data = list?.data
  const version = data?.version

  const nextKeyGen = (key: string) => {
    const g = (keyGen.current.get(key) ?? 0) + 1
    keyGen.current.set(key, g)
    return g
  }
  const setLine = (key: string, l: Line | null) =>
    setLines((ls) => {
      const out = { ...ls }
      if (l) out[key] = l
      else delete out[key]
      return out
    })

  /** The answer is for the object and version shown now. */
  const fits = (v: Value, asked: string | undefined) => v.uid === uid && v.version === asked && listed.current === asked

  const reveal = (key: string) => {
    const g = gen.current
    const kg = nextKeyGen(key)
    const asked = version
    setLine(key, { state: 'reading' })
    client.revealValue(subject, key).then(
      (v) => {
        if (!live.current || g !== gen.current || kg !== keyGen.current.get(key)) return
        if (!fits(v, asked)) {
          setLine(key, { state: 'changed' })
          setReload((n) => n + 1)
          return
        }
        setLine(key, { state: 'shown', v, whole: false })
      },
      (e) => {
        if (!live.current || g !== gen.current || kg !== keyGen.current.get(key)) return
        setLine(key, { state: 'error', text: t('values.readFailed', { class: classLabel(codeOf(e)), detail: detailOf(e) }) })
      },
    )
  }

  const hide = (key: string) => {
    nextKeyGen(key)
    setLine(key, null)
  }
  const hideAll = () => {
    gen.current++
    setLines({})
  }

  const copy = (k: ValueKey) => {
    const asked = version
    const g = gen.current
    // Valid: the section lives, nothing was hidden since, same object and version.
    const valid = () => live.current && g === gen.current && listed.current === asked
    copyValue({
      read: () =>
        client.revealValue(subject, k.key).then((v) => {
          if (!fits(v, asked)) throw new StaleValue()
          return v.value
        }),
      valid,
    }).then(
      (outcome) => {
        if (outcome === 'copied') showNotice(t(k.text ? 'values.copied' : 'values.copiedBase64', { key: k.key }))
        else if (outcome === 'stale' && live.current) setLine(k.key, { state: 'changed' })
      },
      (e) => {
        if (!live.current) return
        if (e instanceof StaleValue) {
          setLine(k.key, { state: 'changed' })
          setReload((n) => n + 1)
        } else setLine(k.key, { state: 'error', text: e instanceof ApiError ? t('values.readFailed', { class: classLabel(e.code), detail: detailOf(e) }) : t('values.copyFailed', { detail: detailOf(e) }) })
      },
    )
  }

  const done = useCallback((res: ValueResult, op: ValueOp, key: string) => {
    setDialog(null)
    if (!res.differs?.length && !res.serverChanges?.length) showNotice(t(op === 'delete' ? 'values.deleted' : 'values.written', { key }))
    setReload((n) => n + 1)
  }, [])

  // A value of a version other than the object's now is never shown: the
  // live revision hides it at once, before the keys are read again.
  const lineOf = (key: string): Line | undefined => {
    const l = lines[key]
    if (l?.state !== 'shown') return l
    return l.v.version === version && (!revision || revision === l.v.version) ? l : { state: 'changed' }
  }
  const shownOf = (key: string): Value | undefined => {
    const l = lineOf(key)
    return l?.state === 'shown' ? l.v : undefined
  }
  const anyShown = data?.keys.some((k) => lineOf(k.key)?.state === 'shown')

  return (
    <section aria-label={t('values.title')} data-values>
      <div className="mb-1.5 flex items-center gap-2">
        <h3 className="text-[12px] font-semibold uppercase tracking-wider text-fg-subtle">{t('values.title')}</h3>
        <span className="flex-1" />
        {anyShown && (
          <button className={toolBtn} onClick={hideAll}>
            {t('values.hideAll')}
          </button>
        )}
        <button className={toolBtn} disabled={!data} onClick={() => setDialog({ op: 'set' })}>
          {t('values.add')}
        </button>
      </div>
      {!list && <p className="text-xs text-fg-subtle">{t('values.loading')}</p>}
      {list?.error && (
        <p role="alert" className="text-xs text-danger">
          {list.error}
        </p>
      )}
      {data && data.keys.length === 0 && <p className="text-xs text-fg-subtle">{t('values.none')}</p>}
      {data && data.keys.length > 0 && (
        <ul className="divide-y divide-line rounded-md border border-line">
          {data.keys.map((k) => (
            <KeyLine
              key={k.key}
              k={k}
              line={lineOf(k.key)}
              onShow={() => reveal(k.key)}
              onHide={() => hide(k.key)}
              onWhole={() => setLines((ls) => (ls[k.key]?.state === 'shown' ? { ...ls, [k.key]: { ...(ls[k.key] as Line & { state: 'shown' }), whole: true } } : ls))}
              onCopy={() => copy(k)}
              onEdit={() => setDialog({ op: 'set', key: k.key })}
              onDelete={() => setDialog({ op: 'delete', key: k.key })}
            />
          ))}
        </ul>
      )}
      <p className="mt-1 text-xs text-fg-subtle">{t('values.hint')}</p>
      {dialog && data && (
        <ValueDialog
          client={client}
          subject={subject}
          list={data}
          mode={dialog}
          shown={dialog.key ? shownOf(dialog.key) : undefined}
          revision={revision}
          kindTitle={kindTitle}
          onClose={() => setDialog(null)}
          onDone={done}
        />
      )}
    </section>
  )
}

/** The object or its version moved on between asking and the answer. */
class StaleValue extends Error {}

function KeyLine(props: {
  k: ValueKey
  line?: Line
  onShow: () => void
  onHide: () => void
  onWhole: () => void
  onCopy: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  const { k, line, onShow, onHide, onWhole, onCopy, onEdit, onDelete } = props
  const shown = line?.state === 'shown' ? line : null
  const text = shown ? (shown.whole || shown.v.value.length <= SHOWN_CHARS ? shown.v.value : shown.v.value.slice(0, SHOWN_CHARS)) : ''
  return (
    <li className="px-2 py-1.5" data-value-key={k.key}>
      <div className="flex flex-wrap items-center gap-1.5">
        <span className="min-w-0 flex-1 break-all font-mono text-[13px]">{k.key}</span>
        <span className="text-xs text-fg-subtle">
          {t('values.bytes', { n: k.size })}
          {!k.text && ` · ${t('values.binary')}`}
        </span>
        {shown || line?.state === 'reading' ? (
          // While reading too: a slow answer can be given up.
          <button className={toolBtn} onClick={onHide}>
            {t('values.hide')}
          </button>
        ) : (
          <button className={toolBtn} onClick={onShow} title={t('values.showHint')}>
            {t('values.show')}
          </button>
        )}
        <button className={toolBtn} onClick={onCopy} title={t('values.copyHint')}>
          {t('values.copy')}
        </button>
        <button className={toolBtn} onClick={onEdit} title={t('values.editHint')}>
          {t('values.edit')}
        </button>
        <button className={`${toolBtn} hover:text-danger`} onClick={onDelete} title={t('values.deleteHint')}>
          {t('values.delete')}
        </button>
      </div>
      {line?.state === 'reading' && <p className="mt-1 text-xs text-fg-subtle">{t('values.reading')}</p>}
      {line?.state === 'changed' && (
        <p role="status" className="mt-1 text-xs text-warning">
          {t('values.changed')}
        </p>
      )}
      {line?.state === 'error' && (
        <p role="alert" className="mt-1 text-xs text-danger">
          {line.text}
        </p>
      )}
      {shown && (
        <>
          <pre data-value className="mt-1 max-h-80 overflow-auto rounded bg-hover/50 px-2 py-1 font-mono text-xs break-all whitespace-pre-wrap">
            {text}
          </pre>
          {text.length < shown.v.value.length && (
            <button className="mt-0.5 text-xs text-accent hover:underline" onClick={onWhole}>
              {t('values.showWhole', { n: shown.v.value.length })}
            </button>
          )}
        </>
      )}
    </li>
  )
}
