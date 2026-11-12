import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './app/App'
import './app/App.css'
import { migrateLegacyStorage } from './lib/legacyStorage'

// Carry over localStorage keys from the former name (dbedit) before anything
// reads them.
migrateLegacyStorage()

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
