package sessionmanager

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"backend/access_control/models"
	"backend/access_control/utility"
	"backend/internal/telemetry"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type LoginPayload struct {
	Username string `json:"username" binding:"omitempty"`
	Password string `json:"password" binding:"omitempty,gte=8"`
}

const session_absolute_exp_duration time.Duration = 3600 * time.Hour

func GenerateSession(user models.User, c *gin.Context) (models.SSOSession, string, error) {
	session_token, err := utility.CryptoRandString(16)
	if err != nil {
		return models.SSOSession{}, "", err
	}
	token_hash := utility.HashToken(session_token)
	return models.SSOSession{
		UserId:           user.ID,
		SessionTokenHash: token_hash,
		Status:           "Active",
		IpAddress:        c.ClientIP(),
		UserAgent:        c.Request.UserAgent(),
		CreatedAt:        time.Now(),
		ExpiresAt:        time.Now().Add(session_absolute_exp_duration),
	}, session_token, nil
}

func UnauthorizedLoginResponse(c *gin.Context, message string) {
	slog.WarnContext(c.Request.Context(), "authentication.login", "status", "rejected")
	newUUID, _ := uuid.NewRandom()
	c.JSON(http.StatusUnauthorized, gin.H{
		"error": gin.H{
			"code":      "LOGIN_FAILED",
			"message":   message,
			"requestId": newUUID.String(),
		},
	})
}

func InternalServerResponse(c *gin.Context) {
	newUUID, _ := uuid.NewRandom()
	c.JSON(http.StatusInternalServerError, gin.H{
		"error": gin.H{
			"code":      "SERVER_ERROR",
			"message":   "Authorization request failed due to a server error",
			"requestId": newUUID.String(),
		},
	})
}

func LoginRequest(c *gin.Context, db *gorm.DB) {
	db = db.WithContext(context.WithoutCancel(c.Request.Context()))
	var loginPayload LoginPayload

	if err := c.ShouldBindJSON(&loginPayload); err != nil {
		UnauthorizedLoginResponse(c, "Invalid login form!")
		return
	}
	var users []models.User
	db.Where("username = ?", loginPayload.Username).Find(&users)

	if len(users) <= 0 {
		UnauthorizedLoginResponse(c, "Wrong username or password!")
		return
	}

	user := users[0]

	if !user.IsActive() {
		UnauthorizedLoginResponse(c, "User account is inactive!")
		return
	}

	if !utility.CheckPasswordHash(loginPayload.Password, user.PasswordHash) {
		UnauthorizedLoginResponse(c, "Wrong username or password!")
		return
	}

	ctx := telemetry.With(c.Request.Context(), telemetry.ID("actor_id", user.ID.String()))
	c.Request = c.Request.WithContext(ctx)
	db = db.WithContext(context.WithoutCancel(ctx))
	session, session_token, err := GenerateSession(users[0], c)

	if err != nil {
		slog.ErrorContext(ctx, "authentication.login", "status", "failed")
		InternalServerResponse(c)
		return
	}

	result := db.Create(&session)
	if err := result.Error; err != nil {
		slog.ErrorContext(ctx, "authentication.login", "status", "failed")
		InternalServerResponse(c)
		return
	}

	db.Model(&models.SSOSession{}).
		Where("user_id = ? AND id != ? AND status = ?", users[0].ID, session.ID, "Active").
		Updates(models.SSOSession{Status: "Revoked", RevokedReason: "new_session_created"})

	slog.InfoContext(ctx, "authentication.login", "status", "succeeded")
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("ssid", session_token, int(session_absolute_exp_duration.Seconds()), "/", "localhost", true, true)
}
