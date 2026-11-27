package migration

import (
	"context"
	"errors"

	"backend/access_control/models"
	sessionmanager "backend/access_control/session_manager"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ApplicationAccess struct{ Platform *gorm.DB }

func (a ApplicationAccess) Identity(c *gin.Context) (string, error) {
	user, err := sessionmanager.CurrentUser(c, a.Platform)
	switch {
	case errors.Is(err, sessionmanager.ErrUnauthenticated):
		return "", err
	case errors.Is(err, sessionmanager.ErrCSRF):
		return "", fault("csrf_failed", err.Error())
	case err != nil:
		return "", fault("authorization_unavailable", "Session validation is unavailable")
	}
	return user.ID.String(), nil
}

func (a ApplicationAccess) Permission(ctx context.Context, user, database string) (Permission, error) {
	if _, err := uuid.Parse(database); err != nil {
		return NoAccess, fault("invalid_input", "Invalid managed database ID")
	}
	effective, err := models.EffectiveAuthorization(ctx, a.Platform, user, database)
	return PermissionFromEffective(effective), err
}
