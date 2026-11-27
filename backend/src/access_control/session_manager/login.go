package sessionmanager

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"backend/access_control/models"
	"backend/access_control/utility"
	"backend/internal/telemetry"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type LoginPayload struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required,gte=8"`
}

func authError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message, "requestId": uuid.NewString()}})
}

func LoginRequest(c *gin.Context, db *gorm.DB) {
	ctx := c.Request.Context()
	c.Header("Cache-Control", "no-store")
	if !validOrigin(c.Request) {
		slog.WarnContext(ctx, "authentication.login", "status", "rejected", "reason", "origin")
		authError(c, http.StatusForbidden, "csrf_failed", ErrCSRF.Error())
		return
	}
	var payload LoginPayload
	if c.ContentType() != "application/json" || c.ShouldBindJSON(&payload) != nil {
		slog.WarnContext(ctx, "authentication.login", "status", "rejected")
		authError(c, http.StatusUnauthorized, "LOGIN_FAILED", "Wrong username or password")
		return
	}

	var token string
	var session models.SSOSession
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&user, "username = ?", payload.Username).Error; err != nil {
			return sessionLookupError(err)
		}
		if !user.IsActive() || !utility.CheckPasswordHash(payload.Password, user.PasswordHash) {
			return ErrUnauthenticated
		}
		ctx = telemetry.With(ctx, telemetry.ID("actor_id", user.ID.String()))
		c.Request = c.Request.WithContext(ctx)
		tx = tx.WithContext(ctx)
		stamp, err := models.AuthorizationStamp(tx, user.ID)
		if err != nil {
			return err
		}
		token, err = utility.CryptoRandString(32)
		if err != nil {
			return err
		}
		now := time.Now()
		session = models.SSOSession{
			UserId: user.ID, SessionTokenHash: utility.HashToken(token), AuthorizationStamp: stamp,
			Status: "Active", IpAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
			CreatedAt: now, ExpiresAt: now.Add(absoluteLifetime), LastActivityAt: &now,
		}
		if err = models.RevokeUserSessions(tx, user.ID, "new_session_created"); err != nil {
			return err
		}
		return tx.Create(&session).Error
	})
	if errors.Is(err, ErrUnauthenticated) {
		slog.WarnContext(ctx, "authentication.login", "status", "rejected")
		authError(c, http.StatusUnauthorized, "LOGIN_FAILED", "Wrong username or password")
		return
	}
	if err != nil {
		slog.LogAttrs(ctx, slog.LevelError, "authentication.login", append(telemetry.ErrorAttrs(err), slog.String("status", "failed"))...)
		authError(c, http.StatusServiceUnavailable, "authorization_unavailable", "Session storage is unavailable")
		return
	}

	setSessionCookie(c, token, session.ExpiresAt)
	slog.InfoContext(ctx, "authentication.login", "status", "succeeded")
	c.Status(http.StatusOK)
}
