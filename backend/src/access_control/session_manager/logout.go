package sessionmanager

import (
	"context"
	"log/slog"
	"net/http"

	"backend/access_control/models"
	"backend/access_control/utility"
	"backend/internal/telemetry"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func UnauthorizedResponse(c *gin.Context, msg string) {
	newUUID, _ := uuid.NewRandom()
	c.JSON(http.StatusUnauthorized, gin.H{
		"error": gin.H{
			"code":      "INVALID_GRANT",
			"message":   msg,
			"requestId": newUUID.String(),
		},
	})
}

func LogoutRequest(c *gin.Context, db *gorm.DB) {
	ctx := c.Request.Context()
	db = db.WithContext(context.WithoutCancel(ctx))
	session_token, err := c.Cookie("ssid")
	if err != nil {
		slog.WarnContext(ctx, "authentication.logout", "status", "rejected")
		UnauthorizedResponse(c, "No session found")
		return
	}

	var sessions []models.SSOSession

	db.Model(&models.SSOSession{}).
		Where("session_token_hash = ? AND status = ?", utility.HashToken(session_token), "Active").
		Find(&sessions)

	if len(sessions) != 1 {
		slog.WarnContext(ctx, "authentication.logout", "status", "failed")
		InternalServerResponse(c)
		return
	}

	ctx = telemetry.With(ctx, telemetry.ID("actor_id", sessions[0].UserId.String()))
	c.Request = c.Request.WithContext(ctx)
	result := db.WithContext(context.WithoutCancel(ctx)).Model(&models.SSOSession{}).
		Where("session_token_hash = ?", utility.HashToken(session_token)).
		Updates(models.SSOSession{Status: "Revoked", RevokedReason: "user_logout"})
	if result.Error != nil {
		slog.LogAttrs(ctx, slog.LevelWarn, "authentication.logout", append(telemetry.ErrorAttrs(result.Error), slog.String("status", "failed"))...)
	} else {
		slog.InfoContext(ctx, "authentication.logout", "status", "succeeded")
	}

	c.SetCookie("ssid", "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Logout successful",
	})
}
