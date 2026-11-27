package models

import (
	"context"
	"time"

	fx "backend/access_control/effect"
	"backend/access_control/utility"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// EffectiveAuthorization separates lookup failures from a valid access denial.
func EffectiveAuthorization(ctx context.Context, db *gorm.DB, userID, databaseID string) (string, error) {
	var effects []string
	err := db.WithContext(ctx).Table("group_policies gp").Select("gp.effect").
		Joins("JOIN user_groups ug ON ug.group_id = gp.group_id").
		Joins("JOIN users u ON u.id = ug.user_id AND u.status = 'Active'").
		Where("ug.user_id = ? AND gp.database_id = ?", userID, databaseID).Pluck("gp.effect", &effects).Error
	if err != nil {
		return "", err
	}
	if fx.Grants(effects, true) {
		return fx.ViewEdit, nil
	}
	if fx.Grants(effects, false) {
		return fx.ViewOnly, nil
	}
	return fx.Blocked, nil
}

const platformAdmin = `EXISTS (
	SELECT 1 FROM user_groups ug JOIN users u ON u.id = ug.user_id AND u.status = 'Active'
	WHERE ug.user_id = ? AND ug.group_id = (
		SELECT (array_agg(id))[1] FROM groups_ WHERE name = 'platform-admins' HAVING count(*) = 1
	)
)`

func HasPlatformAdmin(ctx context.Context, db *gorm.DB, userID string) (bool, error) {
	var admin bool
	err := db.WithContext(ctx).Raw("SELECT "+platformAdmin, userID).Scan(&admin).Error
	return admin, err
}

// AuthorizationStamp tracks the granting inputs for database access, group
// leadership and platform administration. Live access decisions remain authoritative.
func AuthorizationStamp(tx *gorm.DB, userID uuid.UUID) (string, error) {
	var snapshot string
	err := tx.Raw(`
		SELECT jsonb_build_array(`+platformAdmin+`, (
			SELECT jsonb_agg(jsonb_build_array(ug.group_id, gl.leader_id IS NOT NULL,
				gp.database_id, gp.effect) ORDER BY ug.group_id, gp.database_id)
			FROM user_groups ug
			LEFT JOIN group_policies gp ON gp.group_id = ug.group_id
			LEFT JOIN group_leaders gl ON gl.group_id = ug.group_id AND gl.leader_id = ug.user_id
			WHERE ug.user_id = ?
		))::text
	`, userID, userID).Scan(&snapshot).Error
	return utility.HashToken(snapshot), err
}

// RevokeUserSessions is called inside a transaction that holds the account lock.
func RevokeUserSessions(tx *gorm.DB, userID uuid.UUID, reason string) error {
	return tx.Model(&SSOSession{}).Where("user_id = ? AND status = ?", userID, "Active").
		Updates(map[string]any{"status": "Revoked", "revoked_at": time.Now(), "revoked_reason": reason}).Error
}
