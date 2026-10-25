import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '../index.css'
import { HOME_PAGE, fetchMe, safeNext } from '../auth/session'
import Login from '../pages/Login'

const next = safeNext(new URLSearchParams(window.location.search).get('next'))

async function start() {
  let me = null
  try {
    me = await fetchMe()
  } catch {
    /* backend unreachable: show the form, the sign-in attempt will report the problem */
  }
  if (me) {
    window.location.replace(me.admin ? next : HOME_PAGE)
    return
  }
  createRoot(document.getElementById('root')).render(
    <StrictMode>
      <Login next={next} />
    </StrictMode>,
  )
}

start()
