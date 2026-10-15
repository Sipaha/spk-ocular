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
const logWindowID=new URLSearchParams(window.location.search).get('logsWindow')
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    {logWindowID ? <Suspense fallback={null}><LogWindow client={client} id={logWindowID}/></Suspense> : <App client={client} />}
  </StrictMode>,
)
