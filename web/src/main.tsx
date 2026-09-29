import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import { client } from './api/client'
import './index.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App client={client} />
  </StrictMode>,
)
