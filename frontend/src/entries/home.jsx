import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '../index.css'
import { LOGIN_PAGE, fetchMe } from '../auth/session'
import UserHome from '../pages/UserHome'

async function start() {
  const root = createRoot(document.getElementById('root'))
  let me
  try {
    me = await fetchMe()
  } catch (e) {
    root.render(<p className="alert alert-error" role="alert">{e.message}</p>)
    return
  }
  if (!me) {
    window.location.replace(LOGIN_PAGE)
    return
  }
  root.render(
    <StrictMode>
      <UserHome username={me.username} admin={me.admin} />
    </StrictMode>,
  )
}

start()
