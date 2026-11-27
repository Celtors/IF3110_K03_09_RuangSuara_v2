package migration

import (
	"context"

	fx "backend/access_control/effect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Approvers interface {
	RequiredApprovers(ctx context.Context, databaseID string) ([]RequiredApprover, error)
}

type PlatformApprovers struct{ Pool *pgxpool.Pool }

// A leadership row can outlive group membership or account activation.
const groupLeadership = `FROM group_leaders gl
 JOIN user_groups ug ON ug.group_id=gl.group_id AND ug.user_id=gl.leader_id
 JOIN users u ON u.id=gl.leader_id AND u.status='Active'`

const currentGroupLeaders = `SELECT gl.group_id, gl.leader_id::text AS id, u.username ` + groupLeadership

// leaderForApproval selects the authorized leader of the group on approval a.
const leaderForApproval = `SELECT 1 ` + groupLeadership + ` WHERE gl.group_id::text=a.group_id AND gl.leader_id::text=`

func (p PlatformApprovers) RequiredApprovers(ctx context.Context, databaseID string) ([]RequiredApprover, error) {
	if _, err := uuid.Parse(databaseID); err != nil {
		return nil, fault("invalid_input", "Invalid managed database ID")
	}
	if p.Pool == nil {
		return nil, fault("database_unavailable", "Migration storage is unavailable")
	}
	rows, err := p.Pool.Query(ctx, `
 SELECT g.id::text, g.name, COALESCE(leader.id,''), COALESCE(leader.username,'')
 FROM group_policies gp
 JOIN groups_ g ON g.id=gp.group_id
 LEFT JOIN (`+currentGroupLeaders+`) leader ON leader.group_id=g.id
 WHERE gp.database_id=$1::uuid AND gp.effect IN ($2,$3)
 ORDER BY g.name, g.id`, databaseID, fx.ViewOnly, fx.ViewEdit)
	if err != nil {
		return nil, fault("authorization_unavailable", "Group leaders could not be determined")
	}
	defer rows.Close()

	out := []RequiredApprover{}
	for rows.Next() {
		var a RequiredApprover
		if err = rows.Scan(&a.GroupID, &a.GroupName, &a.LeaderID, &a.LeaderUsername); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
