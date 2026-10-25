import { http } from './client'

const CP = '/admin-control-panel'

// Access levels a group can have on a database: the values the schema allows for
// group_policies.effect. Blocked overrides every other group a user belongs to.
export const ACCESS_LEVELS = [
  { value: 'ViewOnly', label: 'View only' },
  { value: 'ViewEdit', label: 'View + edit' },
  { value: 'Blocked', label: 'Blocked' },
]
export const accessLabel = (effect) =>
  ACCESS_LEVELS.find((l) => l.value === effect)?.label ?? effect

export const api = {
  login: (username, password) => http.post('/session-manager/login', { username, password }),
  logout: () => http.post('/session-manager/logout'),
  // Who is signed in and whether they may use the admin console ({ username, admin, admin_access }).
  me: () => http.get('/session-manager/me'),

  listUsers: () => http.get(`${CP}/users`),
  createUser: (username, password) => http.post(`${CP}/users`, { username, password }),
  updateUser: (payload) => http.patch(`${CP}/users`, payload),
  deleteUser: (id) => http.delete(`${CP}/users`, { id }),

  listGroups: () => http.get(`${CP}/groups`),
  createGroup: (name, description) => http.post(`${CP}/groups`, { name, description }),
  updateGroup: (payload) => http.patch(`${CP}/groups`, payload),
  deleteGroup: (id) => http.delete(`${CP}/groups`, { id }),

  groupsOfUser: (id) => http.get(`${CP}/users/groups?id=${encodeURIComponent(id)}`),
  usersOfGroup: (id) => http.get(`${CP}/groups/users?id=${encodeURIComponent(id)}`),
  addMember: (user_id, group_id) => http.post(`${CP}/groups/users`, { user_id, group_id }),
  removeMember: (user_id, group_id) => http.delete(`${CP}/groups/users`, { user_id, group_id }),

  listDatabases: () => http.get(`${CP}/databases`),
  listPolicies: () => http.get(`${CP}/policy`),
  addPolicy: (group_id, database_id, effect) =>
    http.post(`${CP}/policy`, { group_id, database_id, effect }),
  updatePolicy: (group_id, database_id, effect) =>
    http.patch(`${CP}/policy`, { group_id, database_id, effect }),
  removePolicy: (group_id, database_id) => http.delete(`${CP}/policy`, { group_id, database_id }),
}
