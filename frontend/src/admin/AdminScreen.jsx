import Databases from './Databases'
import Groups from './Groups'
import Policies from './Policies'
import Users from './Users'
import AdminShell from './AdminShell'

const PAGES = { users: Users, groups: Groups, databases: Databases, policies: Policies }

// The admin console: one shell (sidebar, toasts) that stays mounted while the section changes.
export default function AdminScreen({ active, me }) {
  const Page = PAGES[active]
  return (
    <AdminShell active={active} username={me.username} access={me.admin_access}>
      <Page />
    </AdminShell>
  )
}
