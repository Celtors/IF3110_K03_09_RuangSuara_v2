package sessionmanager

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"backend/access_control/models"
	"backend/internal/telemetry"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func LogoutRequest(c *gin.Context, db *gorm.DB) {
	ctx := c.Request.Context()
	c.Header("Cache-Control", "no-store")
	if !validOrigin(c.Request) {
		slog.WarnContext(ctx, "authentication.logout", "status", "rejected", "reason", "origin")
		authError(c, http.StatusForbidden, "csrf_failed", ErrCSRF.Error())
		return
	}

	token, _ := c.Cookie(sessionCookie)
	err := ErrUnauthenticated
	if token != "" {
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			user, session, _, err := resolve(tx, token)
			if err != nil {
				return err
			}
			ctx = telemetry.With(ctx, telemetry.ID("actor_id", user.ID.String()))
			c.Request = c.Request.WithContext(ctx)
			if err := ValidateCSRF(c.Request); err != nil {
				return err
			}
			now := time.Now()
			result := tx.WithContext(ctx).Model(&session).Where("status = ?", "Active").Updates(models.SSOSession{
				Status: "Revoked", RevokedReason: "user_logout", RevokedAt: &now,
			})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("session revocation affected an unexpected number of rows")
			}
			return nil
		})
	}
	if errors.Is(err, ErrCSRF) {
		slog.WarnContext(ctx, "authentication.logout", "status", "rejected", "reason", "csrf")
		authError(c, http.StatusForbidden, "csrf_failed", ErrCSRF.Error())
		return
	}
	if err != nil && !errors.Is(err, ErrUnauthenticated) {
		slog.LogAttrs(ctx, slog.LevelWarn, "authentication.logout", append(telemetry.ErrorAttrs(err), slog.String("status", "failed"))...)
		authError(c, http.StatusServiceUnavailable, "authorization_unavailable", "Could not revoke the session. Try signing out again.")
		return
	}

	setSessionCookie(c, "", time.Unix(1, 0))
	if err == nil {
		slog.InfoContext(ctx, "authentication.logout", "status", "succeeded")
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Logout successful"})
		return
	}
	slog.InfoContext(ctx, "authentication.logout", "status", "already_inactive")
	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "No active session"})
}
