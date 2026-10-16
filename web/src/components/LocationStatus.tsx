import { createContext, useContext, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

const LocationActiveContext = createContext(true)

export function LocationStatusScope({ active = true, children }: { active?: boolean; children: ReactNode }) {
  return <LocationActiveContext value={active}>{children}</LocationActiveContext>
}

const LocationStatusContext = createContext<HTMLElement | null | undefined>(undefined)

/** A single destination for the active location, without copying its state. */
export function LocationStatusProvider({ children }: { children: (slot: (element: HTMLDivElement | null) => void) => ReactNode }) {
  const [element, setElement] = useState<HTMLDivElement | null>(null)
  return <LocationStatusContext value={element}>{children(setElement)}</LocationStatusContext>
}

/** Detached viewers keep their own status; inactive dock tabs publish nothing. */
export function LocationStatus({ active = true, children }: { active?: boolean; children: ReactNode }) {
  const enabled = useContext(LocationActiveContext)
  const element = useContext(LocationStatusContext)
  if (element === undefined) return <div className="log-status flex shrink-0 items-center gap-3 border-t border-line px-3 py-0.5 text-[12px] text-fg-subtle">{children}</div>
  return active && enabled && element ? createPortal(children, element) : null
}
