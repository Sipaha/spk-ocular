import { useState, type ReactNode } from 'react'
import type { ActionList } from '../api/types'
import { messageText, t } from '../i18n'

/** Long lists are shown in portions: every item stays reachable, none is dropped. */
export const PORTION = 50

/** The first n items, and a button for the next portion while any are left. */
export function Portions<T>({ items, render }: { items: T[]; render: (shown: T[]) => ReactNode }) {
  const [n, setN] = useState(PORTION)
  const left = items.length - n
  return (
    <>
      {render(items.slice(0, n))}
      {left > 0 && (
        <button type="button" className="mt-1 text-xs text-accent hover:underline" onClick={() => setN((x) => x + PORTION)}>
          {t('action.showMore', { n: Math.min(PORTION, left), left })}
        </button>
      )}
    </>
  )
}

/** The objects a plan concerns, by group; a collapsed group opens on request. */
export function ActionLists({ lists }: { lists: ActionList[] }) {
  return (
    <>
      {lists.map((l, i) => (
        <OneList key={i} list={l} />
      ))}
    </>
  )
}

function OneList({ list }: { list: ActionList }) {
  const [open, setOpen] = useState(!list.collapsed)
  const title = `${messageText(list.title)} (${list.items.length})`
  const head = 'text-[12px] font-semibold uppercase tracking-wider'
  return (
    <section aria-label={title} className={list.destructive ? 'text-danger' : ''}>
      {list.collapsed ? (
        <h3 className={`mb-1 ${head}`}>
          <button type="button" aria-expanded={open} className="uppercase tracking-wider hover:underline" onClick={() => setOpen((o) => !o)}>
            <span aria-hidden>{open ? '▾ ' : '▸ '}</span>
            {title}
          </button>
        </h3>
      ) : (
        <h3 className={`mb-1 ${list.destructive ? '' : 'text-fg-subtle'} ${head}`}>{title}</h3>
      )}
      {open && (
        <Portions
          items={list.items}
          render={(shown) => (
            <ul className="space-y-0.5 pl-5 font-mono text-xs">
              {shown.map((it, j) => (
                <li key={j} className="break-all">
                  {it.name}
                  {it.note && <span className="font-sans text-fg-muted"> · {messageText(it.note)}</span>}
                </li>
              ))}
            </ul>
          )}
        />
      )}
    </section>
  )
}
