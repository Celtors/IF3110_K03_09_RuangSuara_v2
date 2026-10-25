import { useCallback, useMemo, useState } from 'react'
import { api } from '../api/endpoints'
import { AuthContext } from './auth-context'

const KEY = 'ruangsuara.user'

function readStored() {
  try {
    return sessionStorage.getItem(KEY)
  } catch {
    return null
  }
}
function writeStored(value) {
  try {
    if (value) sessionStorage.setItem(KEY, value)
    else sessionStorage.removeItem(KEY)
  } catch {
    
  }
}

export default function AuthProvider({ children }) {
  const [username, setUsername] = useState(readStored)

  const login = useCallback(async (name, password) => {
    await api.login(name, password)
    writeStored(name)
    setUsername(name)
  }, [])

  const logout = useCallback(async () => {
    try {
      await api.logout()
    } catch {

    }
    writeStored(null)
    setUsername(null)
  }, [])

  const value = useMemo(() => ({ username, login, logout }), [username, login, logout])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}
