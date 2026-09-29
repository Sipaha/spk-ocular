// The regex search worker (see regexSearch.ts). Terminated by the page when
// a pattern runs past its time budget.
import { workerCore, type ToWorker } from './regexSearch'

const handle = workerCore((m) => postMessage(m))
onmessage = (e: MessageEvent<ToWorker>) => handle(e.data)
