import type { ReactNode } from 'react'

/** List identity stays accessible; visible title and count live in LocationStatus. */
export function WorkspaceToolbar({ title, count, children }: { title: string; count?: ReactNode; children: ReactNode }) {
 return <header className="resource-toolbar resource-list-toolbar">
  <h1 className="sr-only">{title}</h1>
  {count !== undefined && <span className="sr-only" aria-label="count">{count}</span>}
  {children}
 </header>
}
