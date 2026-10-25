package models

import (
	"context"

	fx "backend/access_control/effect"
	"gorm.io/gorm"
)

// EffectiveAuthorization resolves all database policies in one query. Query
// failures are returned separately from a valid decision denying access.
func EffectiveAuthorization(ctx context.Context, db *gorm.DB, userID, databaseID string) (string, error) {
	var effects []string
	err := db.WithContext(ctx).Table("group_policies gp").Select("gp.effect").
		Joins("JOIN user_groups ug ON ug.group_id = gp.group_id").
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
