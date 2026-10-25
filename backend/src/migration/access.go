package migration

import (
	"context"
	"errors"
	"net/http"

	"backend/access_control/models"
	sessionmanager "backend/access_control/session_manager"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ApplicationAccess struct{ Platform *gorm.DB }

func (a ApplicationAccess) Identity(request *http.Request) (string, error) {
	user, err := sessionmanager.CurrentUser(request.Context(), request, a.Platform)
	if err != nil {
		if !errors.Is(err, sessionmanager.ErrUnauthenticated) && !errors.Is(err, sessionmanager.ErrNoSession) {
			return "", fault("authorization_unavailable", "Session validation is unavailable")
		}
		return "", err
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

func (ApplicationAccess) CSRF(request *http.Request) error {
	return sessionmanager.ValidateCSRF(request)
}
