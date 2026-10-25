import { http } from './client'

const CP = '/admin-control-panel'

// Access levels a group can have on a database. The backend stores `effect` as a
// free string (VARCHAR(10)), so these two values are the contract of this UI.
export const ACCESS_LEVELS = [
  { value: 'ViewOnly', label: 'View only' },
  { value: 'ViewEdit', label: 'View + edit' },
]
export const accessLabel = (effect) =>
  ACCESS_LEVELS.find((l) => l.value === effect)?.label ?? effect

export const api = {
  login: (username, password) => http.post('/session-manager/login', { username, password }),
  logout: () => http.post('/session-manager/logout'),

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

  listPolicies: () => http.get(`${CP}/policy`),
  // The backend always creates a policy as "Allow"; the chosen level is applied with a PATCH.
  addPolicy: async (group_id, database_id, effect) => {
    await http.post(`${CP}/policy`, { group_id, database_id })
    await http.patch(`${CP}/policy`, { group_id, database_id, effect })
  },
  updatePolicy: (group_id, database_id, effect) =>
    http.patch(`${CP}/policy`, { group_id, database_id, effect }),
  removePolicy: (group_id, database_id) => http.delete(`${CP}/policy`, { group_id, database_id }),
}
