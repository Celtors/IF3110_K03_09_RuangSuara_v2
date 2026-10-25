import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '../index.css'
import { getUsername, safeNext } from '../auth/session'
import Login from '../pages/Login'

const next = safeNext(new URLSearchParams(window.location.search).get('next'))

if (getUsername()) {
  window.location.replace(next)
} else {
  createRoot(document.getElementById('root')).render(
    <StrictMode>
      <Login next={next} />
    </StrictMode>,
  )
}
