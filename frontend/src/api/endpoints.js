import { http } from './client'

const CP = '/admin-control-panel'

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

  listDatabases: () => http.get(`${CP}/databases`),
  listPolicies: () => http.get(`${CP}/policy`),
  addPolicy: async (group_id, database_id, effect) => {
    await http.post(`${CP}/policy`, { group_id, database_id })
    await http.patch(`${CP}/policy`, { group_id, database_id, effect })
  },
  updatePolicy: (group_id, database_id, effect) =>
    http.patch(`${CP}/policy`, { group_id, database_id, effect }),
  removePolicy: (group_id, database_id) => http.delete(`${CP}/policy`, { group_id, database_id }),
}
