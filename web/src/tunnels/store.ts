import { create } from 'zustand'
import type { Client } from '../api/client'
import type { Tunnel } from '../api/types'

// Tunnels belong to the app: the list is reloaded on 'forwards_changed'
// (a state change at once, counters at most once a second).

export interface TunnelsState {
  list: Tunnel[]
  panel: boolean
  error: string | null
}

export const useTunnels = create<TunnelsState>(() => ({ list: [], panel: false, error: null }))

/** Reloads are sequenced: an older, slower answer never overwrites a newer one. */
export function tunnelLoader(client: Client) {
  let inFlight = false
  let again = false
  return async function load() {
    if (inFlight) {
      again = true
      return
    }
    inFlight = true
    try {
      do {
        again = false
        try {
          useTunnels.setState({ list: await client.listForwards(), error: null })
        } catch (e) {
          useTunnels.setState({ error: e instanceof Error ? e.message : String(e) })
        }
      } while (again)
    } finally {
      inFlight = false
    }
  }
}

export const tunnels = {
  showPanel(on: boolean) {
    useTunnels.setState({ panel: on })
  },
}

export const tunnelURL = (tn: Tunnel) => `${tn.scheme}://127.0.0.1:${tn.localPort}/`
