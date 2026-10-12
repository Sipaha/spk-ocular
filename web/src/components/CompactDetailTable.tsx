import type { ComponentProps, ReactNode } from 'react'

interface DetailColumn {
  id: string
  width?: number
  /** Omit headings for an existing headerless detail list. */
  label?: string
}

/** Bounded read-only details keep semantic tables, without resource-grid state. */
export function CompactDetailTable({ label, columns, minWidth, className = '', children }: {
  label: string
  columns: DetailColumn[]
  minWidth: number
  className?: string
  children: ReactNode
}) {
  return <div className="overflow-x-auto rounded border border-line" tabIndex={0} aria-label={label}>
    <table className={`w-full table-fixed text-left ${className}`} style={{ minWidth }} aria-label={label}>
      <colgroup>{columns.map(column => <col key={column.id} style={column.width === undefined ? undefined : { width: column.width }} />)}</colgroup>
      {columns.some(column => column.label !== undefined) && <thead className="bg-hover/50 text-[11px] text-fg-subtle"><tr>
        {columns.map(column => <th key={column.id} scope="col" title={column.label} className="truncate px-2 py-1.5 font-medium">{column.label}</th>)}
      </tr></thead>}
      <tbody className="divide-y divide-line">{children}</tbody>
    </table>
  </div>
}

export function CompactDetailRow({ className = '', ...props }: ComponentProps<'tr'>) {
  return <tr {...props} className={`h-8 hover:bg-hover/50 ${className}`} />
}

/** Small detail actions retain their feature-owned handlers and localized names. */
export function CompactDetailAction({ label, className = 'text-fg-muted', ...props }: ComponentProps<'button'> & { label: string }) {
  return <button {...props} type={props.type ?? 'button'} aria-label={label} title={props.title ?? label}
    className={`flex h-6 w-6 items-center justify-center rounded outline-none hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-40 ${className}`} />
}
