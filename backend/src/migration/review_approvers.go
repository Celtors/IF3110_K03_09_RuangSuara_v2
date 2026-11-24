package migration

import (
	"context"

	fx "backend/access_control/effect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Approvers decides whose approval a change request needs.
type Approvers interface {
	// RequiredApprovers returns every group with ViewOnly or ViewEdit access to the
	// database, with the leader of each. A Blocked group is not a reviewer. A group
	// without a leader is returned with an empty LeaderID so the caller can refuse.
	RequiredApprovers(ctx context.Context, databaseID string) ([]RequiredApprover, error)
}

// PlatformApprovers reads group policies and leaders from the platform database,
// the same database that holds the migration storage.
type PlatformApprovers struct{ Pool *pgxpool.Pool }

func (p PlatformApprovers) RequiredApprovers(ctx context.Context, databaseID string) ([]RequiredApprover, error) {
	if _, err := uuid.Parse(databaseID); err != nil {
		return nil, fault("invalid_input", "Invalid managed database ID")
	}
	if p.Pool == nil {
		return nil, fault("database_unavailable", "Migration storage is unavailable")
	}
	rows, err := p.Pool.Query(ctx, `
 SELECT g.id::text, g.name, COALESCE(gl.leader_id::text,''), COALESCE(u.username,''), COALESCE(u.status='Active', false)
 FROM group_policies gp
 JOIN groups_ g ON g.id=gp.group_id
 LEFT JOIN group_leaders gl ON gl.group_id=g.id
 LEFT JOIN users u ON u.id=gl.leader_id
 WHERE gp.database_id=$1::uuid AND gp.effect IN ($2,$3)
 ORDER BY g.name, g.id`, databaseID, fx.ViewOnly, fx.ViewEdit)
	if err != nil {
		return nil, fault("authorization_unavailable", "Group leaders could not be determined")
	}
	defer rows.Close()

	out := []RequiredApprover{}
	for rows.Next() {
		var a RequiredApprover
		if err = rows.Scan(&a.GroupID, &a.GroupName, &a.LeaderID, &a.LeaderUsername, &a.LeaderActive); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
