
// A user id, or "You" when it is the signed-in user.
// The API only names group leaders (in approvals), so that is the only place usernames come from.
export function namesFrom(requests) {
    const names = {};
    for (const r of requests ?? [])
        for (const a of r.approvals ?? []) if (a.required_leader_id && a.leader_username) names[a.required_leader_id] = a.leader_username;
    return names;
}