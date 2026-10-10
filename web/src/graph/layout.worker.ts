import type { ClusterGraph } from '../api/types'
import { layoutGraph } from './model'
self.onmessage = (event: MessageEvent<{graph: ClusterGraph; seq: number}>) => {
 self.postMessage({layout: layoutGraph(event.data.graph), seq: event.data.seq})
}
