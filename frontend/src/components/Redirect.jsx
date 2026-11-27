import { useEffect } from 'react'
import { navigate } from '../router'

// Replaces the current history entry with `to`, so the back button does not return to a
// screen the user was not allowed to see.
export default function Redirect({ to }) {
  useEffect(() => {
    navigate(to, { replace: true })
  }, [to])
  return null
}
