package databasemanagement

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	fx "backend/access_control/effect"
	acmodels "backend/access_control/models"
	sessionmanager "backend/access_control/session_manager"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Handler struct {
	platform *gorm.DB
	provider *Provider
}

func PlatformAdminMiddlewares(platform *gorm.DB) []gin.HandlerFunc {
	h := &Handler{platform: platform}
	return []gin.HandlerFunc{h.requireSession, h.requireAdmin}
}

func RegisterRoutes(r gin.IRouter, platform *gorm.DB, provider *Provider) {
	h := &Handler{platform: platform, provider: provider}
	admin := r.Group("/api/admin/databases", h.requireSession, h.requireAdmin)
	admin.POST("", h.createDatabase)
	admin.GET("", h.listDatabases)
	admin.GET("/:id", h.getDatabase)
	admin.PATCH("/:id", h.updateDatabase)
	admin.DELETE("/:id", h.deleteDatabase)
	admin.POST("/:id/test", h.testConnection)
	admin.PUT("/:id/groups/:groupID/policy", h.setPolicy)
	r.GET("/session-manager/me", h.requireSession, h.me)
	r.GET("/api/accessible-databases", h.requireSession, h.accessibleDatabases)
	viewer := r.Group("/api/databases/:id", h.requireSession, h.requireView)
	viewer.GET("/schemas", h.schemas)
	viewer.GET("/schemas/:schema/tables", h.tables)
	viewer.GET("/schemas/:schema/tables/:table/columns", h.columns)
	viewer.GET("/schemas/:schema/tables/:table/constraints", h.constraints)
	viewer.GET("/schemas/:schema/tables/:table/indexes", h.indexes)
	viewer.GET("/schemas/:schema/tables/:table/rows", h.rows)
}
func fail(c *gin.Context, status int, code, message string) {
	slog.WarnContext(c.Request.Context(), "database.request_failed", "status", "failed", "reason", code, "route", c.FullPath())
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}
func (h *Handler) requireSession(c *gin.Context) {
	user, err := sessionmanager.CurrentUser(c, h.platform)
	switch {
	case errors.Is(err, sessionmanager.ErrUnauthenticated):
		fail(c, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid session is required")
		return
	case errors.Is(err, sessionmanager.ErrCSRF):
		fail(c, http.StatusForbidden, "csrf_failed", err.Error())
		return
	case err != nil:
		fail(c, http.StatusServiceUnavailable, "authorization_unavailable", "Session storage is unavailable")
		return
	}
	c.Set("principal_id", user.ID.String())
	c.Set("principal", user)
	c.Next()
}

func (h *Handler) requireAdmin(c *gin.Context) {
	id, _ := c.Get("principal_id")
	ctx := c.Request.Context()
	admin, err := acmodels.HasPlatformAdmin(ctx, h.platform, fmt.Sprint(id))
	if err != nil {
		fail(c, http.StatusServiceUnavailable, "authorization_unavailable", "Admin authorization is unavailable")
		return
	}
	if !admin {
		slog.WarnContext(ctx, "authorization.admin", "status", "denied")
		fail(c, http.StatusForbidden, "FORBIDDEN", "You do not have access to the admin console")
		return
	}
	slog.InfoContext(ctx, "authorization.admin", "status", "allowed")
	c.Next()
}

func (h *Handler) me(c *gin.Context) {
	user := c.MustGet("principal").(acmodels.User)
	admin, err := acmodels.HasPlatformAdmin(c.Request.Context(), h.platform, user.ID.String())
	if err != nil {
		fail(c, http.StatusServiceUnavailable, "authorization_unavailable", "Admin authorization is unavailable")
		return
	}
	access := "none"
	if admin {
		access = fx.ViewEdit
	}
	c.Header("Cache-Control", "no-store")
	c.Header(sessionmanager.CSRFHeader, c.GetString(sessionmanager.CSRFHeader))
	c.JSON(http.StatusOK, gin.H{"id": user.ID, "username": user.Username, "admin": admin, "admin_access": access})
}

func (h *Handler) requireView(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_ID", "Database ID is invalid")
		return
	}
	principal, _ := c.Get("principal_id")
	ctx := c.Request.Context()
	permission, err := acmodels.EffectiveAuthorization(ctx, h.platform, fmt.Sprint(principal), id)
	if err != nil {
		fail(c, http.StatusServiceUnavailable, "authorization_unavailable", "Database authorization is unavailable")
		return
	}
	if permission != fx.ViewEdit && permission != fx.ViewOnly {
		slog.WarnContext(ctx, "authorization.database", "status", "denied", "required_permission", "view")
		fail(c, http.StatusForbidden, "FORBIDDEN", "You do not have view access to this database")
		return
	}
	slog.InfoContext(ctx, "authorization.database", "status", "allowed", "required_permission", "view")
	c.Next()
}

// policyGrant is one group policy that applies to a user for a database.
type policyGrant struct {
	Effect string
}

// effectivePermission combines the effects a user gets through all of their groups:
// Blocked in any group denies, ViewEdit allows view and edit, ViewOnly allows view only.
func effectivePermission(rows []policyGrant, edit bool) bool {
	effects := make([]string, len(rows))
	for i, row := range rows {
		effects[i] = row.Effect
	}
	return fx.Grants(effects, edit)
}

func (h *Handler) listDatabases(c *gin.Context) {
	var list []Database
	if err := h.platform.WithContext(context.WithoutCancel(c.Request.Context())).Order("created_at DESC").Find(&list).Error; err != nil {
		fail(c, 500, "DATABASE_LIST_FAILED", "Could not list databases")
		return
	}
	c.JSON(200, list)
}
func (h *Handler) getDatabase(c *gin.Context) {
	var d Database
	if err := h.platform.WithContext(context.WithoutCancel(c.Request.Context())).First(&d, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, 404, "DATABASE_NOT_FOUND", "Database not found")
		return
	}
	c.JSON(200, d)
}
func (h *Handler) setPolicy(c *gin.Context) {
	groupID, err := uuid.Parse(c.Param("groupID"))
	if err != nil {
		fail(c, 400, "INVALID_ID", "Group ID is invalid")
		return
	}
	databaseID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		fail(c, 400, "INVALID_ID", "Database ID is invalid")
		return
	}
	var req PolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.GroupID != groupID {
		fail(c, 400, "INVALID_POLICY", "A valid group ID and effect are required")
		return
	}
	if !fx.IsValid(req.Effect) {
		fail(c, 400, "INVALID_POLICY", "Effect must be ViewOnly, ViewEdit or Blocked")
		return
	}
	policy := GroupPolicy{GroupID: groupID, DatabaseID: databaseID, Effect: req.Effect}
	err = h.platform.WithContext(context.WithoutCancel(c.Request.Context())).
		Where("group_id = ? AND database_id = ?", groupID, databaseID).Assign(policy).FirstOrCreate(&policy).Error
	if err != nil {
		fail(c, 400, "POLICY_UPDATE_FAILED", "Could not save group policy")
		return
	}
	slog.InfoContext(c.Request.Context(), "database.policy_saved", "status", "succeeded")
	c.JSON(200, policy)
}
