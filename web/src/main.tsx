import { StrictMode, lazy, Suspense } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import { client } from './api/client'
import './index.css'
import { installFocusMode } from './focusMode'
import { installTooltips } from './tooltips'

const removeFocusMode=installFocusMode(document)
const removeTooltips=installTooltips(document)
if(import.meta.hot)import.meta.hot.dispose(()=>{removeFocusMode();removeTooltips()})

const LogWindow=lazy(()=>import('./logs/LogWindow').then(module=>({default:module.LogWindow})))
const TerminalWindow=lazy(()=>import('./term/TerminalWindow'))
const FilesWindow=lazy(()=>import('./files/FilesWindow'))
const toolWindowID=new URLSearchParams(window.location.search).get('toolWindow')
const toolKind=new URLSearchParams(window.location.search).get('toolKind')
const logWindowID=new URLSearchParams(window.location.search).get('logsWindow')
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    {toolWindowID&&toolKind==='shell'?<Suspense fallback={null}><TerminalWindow client={client} id={toolWindowID}/></Suspense>:toolWindowID&&toolKind==='files'?<Suspense fallback={null}><FilesWindow client={client} id={toolWindowID}/></Suspense>:logWindowID ? <Suspense fallback={null}><LogWindow client={client} id={logWindowID}/></Suspense> : <App client={client} />}
  </StrictMode>,
)
