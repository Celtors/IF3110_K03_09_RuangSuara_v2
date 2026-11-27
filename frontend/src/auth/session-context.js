import { createContext, useContext } from 'react'

// { me, error, signIn, signOut, refresh } — see SessionProvider.
export const SessionContext = createContext(null)

export function useSession() {
  const ctx = useContext(SessionContext)
  if (!ctx) throw new Error('useSession must be used inside <SessionProvider>')
  return ctx
}
