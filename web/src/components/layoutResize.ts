import { useSyncExternalStore } from 'react'

// Layout gestures defer log ingestion only while the pointer owns a separator.
// The stream continues reading into its existing bounded pending buffer.
let resizing = false
const listeners = new Set<() => void>()
export const layoutResizing = () => resizing
export function setLayoutResizing(next: boolean) {
 if (resizing === next) return
 resizing = next
 listeners.forEach(listener => listener())
}
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener) } }
export const useLayoutResizing = () => useSyncExternalStore(subscribe,layoutResizing)
