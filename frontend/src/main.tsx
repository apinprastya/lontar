import React from 'react'
import ReactDOM from 'react-dom/client'
import '@fontsource/ibm-plex-sans/400.css'
import '@fontsource/ibm-plex-sans/500.css'
import '@fontsource/ibm-plex-sans/600.css'
import '@fontsource/ibm-plex-sans/400-italic.css'
import App from './app/App'
import './app/App.css'
import { migrateLegacyStorage } from './lib/legacyStorage'
import { flushUIState, initUIState } from './lib/uiState'
import { uiStateBackend } from './lib/uiStateBackend'

// Carry over localStorage keys from the former name (dbedit) before anything
// reads them.
migrateLegacyStorage()

// Panel layouts and column widths are read synchronously while rendering,
// so the saved UI state is loaded first (initUIState never rejects).
void initUIState(uiStateBackend).then(() => {
  window.addEventListener('pagehide', () => void flushUIState())
  ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
    <React.StrictMode>
      <App />
    </React.StrictMode>,
  )
})
