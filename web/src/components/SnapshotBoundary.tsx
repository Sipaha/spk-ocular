import type { ReactNode } from 'react'
import { CancelledList, ListLoading } from './ListLoading'
import type { useSnapshotRequest } from '../views/useSnapshotRequest'

/** Keep retained content mounted and geometry stable through read and cancel. */
export function SnapshotBoundary({ request, children }: { request: ReturnType<typeof useSnapshotRequest<unknown>>; children: ReactNode }) {
 const waiting = request.busy && request.value === null
 const hidden = request.cancelled || waiting
 return <div className="relative flex min-h-0 min-w-0 flex-1">
  <div className="relative flex min-h-0 min-w-0 flex-1" inert={hidden} aria-hidden={hidden || undefined} style={hidden ? {visibility:'hidden'} : undefined} aria-busy={request.busy}>{children}</div>
  {waiting && <div className="absolute inset-0 z-20 flex items-center justify-center bg-panel"><ListLoading view={request.loading}/></div>}
  {request.cancelled && <div className="absolute inset-0 z-30 flex flex-col"><CancelledList retry={request.refresh}/></div>}
 </div>
}
