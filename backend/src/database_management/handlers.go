package databasemanagement

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	fx "backend/access_control/effect"
	acmodels "backend/access_control/models"
	"backend/access_control/utility"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Handler struct {
	platform *gorm.DB
	provider *Provider
}

func PlatformAdminMiddlewares(platform *gorm.DB) []gin.HandlerFunc {
	h := &Handler{platform: platform, provider: NewProvider(platform)}
	return []gin.HandlerFunc{h.requireSession, h.requireAdmin}
}

func RegisterRoutes(r gin.IRouter, platform *gorm.DB) {
	h := &Handler{platform: platform, provider: NewProvider(platform)}
	admin := r.Group("/api/admin/databases", h.requireSession, h.requireAdmin)
	admin.POST("", h.createDatabase)
	admin.GET("", h.listDatabases)
	admin.GET("/:id", h.getDatabase)
	admin.PATCH("/:id", h.updateDatabase)
	admin.DELETE("/:id", h.deleteDatabase)
	admin.POST("/:id/test", h.testConnection)
	admin.PUT("/:id/groups/:groupID/policy", h.setPolicy)
	viewer := r.Group("/api/databases/:id", h.requireSession, h.requireView)
	viewer.GET("/schemas", h.schemas)
	viewer.GET("/schemas/:schema/tables", h.tables)
	viewer.GET("/schemas/:schema/tables/:table/columns", h.columns)
	viewer.GET("/schemas/:schema/tables/:table/constraints", h.constraints)
	viewer.GET("/schemas/:schema/tables/:table/indexes", h.indexes)
	viewer.GET("/schemas/:schema/tables/:table/rows", h.rows)
}
func fail(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}
func (h *Handler) requireSession(c *gin.Context) {
	token, err := c.Cookie("ssid")
	if err != nil || token == "" {
		fail(c, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid session is required")
		return
	}
	var session acmodels.SSOSession
	if err := h.platform.Where("session_token_hash = ? AND status = ?", utility.HashToken(token), "Active").First(&session).Error; err != nil || !session.IsValid() {
		fail(c, http.StatusUnauthorized, "INVALID_SESSION", "A valid session is required")
		return
	}
	var user acmodels.User
	if err := h.platform.Where("id = ? AND status = ?", session.UserId, "Active").First(&user).Error; err != nil {
		fail(c, http.StatusUnauthorized, "INVALID_SESSION", "A valid session is required")
		return
	}
	c.Set("principal_id", user.ID.String())
	c.Next()
}
func (h *Handler) requireAdmin(c *gin.Context) {
	id, _ := c.Get("principal_id")
	for _, candidate := range strings.Split(os.Getenv("PLATFORM_ADMIN_USER_IDS"), ",") {
		if strings.TrimSpace(candidate) == fmt.Sprint(id) && candidate != "" {
			c.Next()
			return
		}
	}
	fail(c, http.StatusForbidden, "FORBIDDEN", "Platform Admin role is required")
}
func (h *Handler) requireView(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_ID", "Database ID is invalid")
		return
	}
	principal, _ := c.Get("principal_id")
	if !h.hasPermission(fmt.Sprint(principal), id, false) {
		fail(c, http.StatusForbidden, "FORBIDDEN", "You do not have view access to this database")
		return
	}
	c.Next()
}
func (h *Handler) isAdmin(id string) bool {
	for _, v := range strings.Split(os.Getenv("PLATFORM_ADMIN_USER_IDS"), ",") {
		if strings.TrimSpace(v) == id && v != "" {
			return true
		}
	}
	return false
}

type policyGrant struct {
	Effect string
}

func effectivePermission(rows []policyGrant, edit bool) bool {
	effects := make([]string, len(rows))
	for i, row := range rows {
		effects[i] = row.Effect
	}
	return fx.Grants(effects, edit)
}

func (h *Handler) hasPermission(userID, databaseID string, edit bool) bool {
	var rows []policyGrant
	err := h.platform.Table("group_policies gp").Select("gp.effect").Joins("JOIN user_groups ug ON ug.group_id = gp.group_id").Where("ug.user_id = ? AND gp.database_id = ?", userID, databaseID).Scan(&rows).Error
	if err != nil {
		return false
	}
	return effectivePermission(rows, edit)
}
func (h *Handler) createDatabase(c *gin.Context) {
	var req CreateDatabaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "INVALID_REQUEST", "Invalid database configuration")
		return
	}
	if err := req.Validate(); err != nil {
		fail(c, 400, "INVALID_REQUEST", err.Error())
		return
	}
	mode := normalizeSSLMode(req.SSLMode)
	if validateSSLMode(mode) == "" {
		fail(c, 400, "INVALID_REQUEST", "Unsupported SSL mode")
		return
	}
	ciphertext, err := encryptPassword(req.Password)
	if err != nil {
		fail(c, 500, "CREDENTIALS_NOT_CONFIGURED", "Secure credential encryption is not configured")
		return
	}
	db := Database{Name: strings.TrimSpace(req.Name), Host: strings.TrimSpace(req.Host), Port: req.Port, DatabaseName: req.DatabaseName, Username: req.Username, PasswordCiphertext: ciphertext, SSLMode: mode}
	if err := h.platform.Create(&db).Error; err != nil {
		fail(c, 500, "DATABASE_CREATE_FAILED", "Could not register database")
		return
	}
	c.JSON(http.StatusCreated, db)
}
func (h *Handler) listDatabases(c *gin.Context) {
	var list []Database
	if err := h.platform.Order("created_at DESC").Find(&list).Error; err != nil {
		fail(c, 500, "DATABASE_LIST_FAILED", "Could not list databases")
		return
	}
	c.JSON(200, list)
}
func (h *Handler) getDatabase(c *gin.Context) {
	var d Database
	if err := h.platform.First(&d, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, 404, "DATABASE_NOT_FOUND", "Database not found")
		return
	}
	c.JSON(200, d)
}
func (h *Handler) updateDatabase(c *gin.Context) {
	var d Database
	if err := h.platform.First(&d, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, 404, "DATABASE_NOT_FOUND", "Database not found")
		return
	}
	var req UpdateDatabaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "INVALID_REQUEST", "Invalid update request")
		return
	}
	if req.Name != "" {
		d.Name = req.Name
	}
	if req.Host != "" {
		if err := validateHost(req.Host); err != nil {
			fail(c, 400, "INVALID_REQUEST", err.Error())
			return
		}
		d.Host = req.Host
	}
	if req.Port != 0 {
		if req.Port < 1 || req.Port > 65535 {
			fail(c, 400, "INVALID_REQUEST", "Invalid port")
			return
		}
		d.Port = req.Port
	}
	if req.DatabaseName != "" {
		d.DatabaseName = req.DatabaseName
	}
	if req.Username != "" {
		d.Username = req.Username
	}
	if req.SSLMode != "" {
		if validateSSLMode(req.SSLMode) == "" {
			fail(c, 400, "INVALID_REQUEST", "Unsupported SSL mode")
			return
		}
		d.SSLMode = req.SSLMode
	}
	if req.Password != "" {
		enc, err := encryptPassword(req.Password)
		if err != nil {
			fail(c, 500, "CREDENTIALS_NOT_CONFIGURED", "Secure credential encryption is not configured")
			return
		}
		d.PasswordCiphertext = enc
	}
	if err := h.platform.Save(&d).Error; err != nil {
		fail(c, 500, "DATABASE_UPDATE_FAILED", "Could not update database")
		return
	}
	c.JSON(200, d)
}
func (h *Handler) deleteDatabase(c *gin.Context) {
	result := h.platform.Delete(&Database{}, "id = ?", c.Param("id"))
	if result.Error != nil {
		fail(c, 500, "DATABASE_DELETE_FAILED", "Could not delete database")
		return
	}
	if result.RowsAffected == 0 {
		fail(c, 404, "DATABASE_NOT_FOUND", "Database not found")
		return
	}
	c.Status(204)
}
func (h *Handler) testConnection(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 6*time.Second)
	defer cancel()
	db, err := h.provider.Target(ctx, c.Param("id"))
	if err != nil {
		c.JSON(200, gin.H{"success": false, "message": "Connection failed. Verify target settings, credentials, network access, and TLS configuration."})
		return
	}
	closeTarget(db)
	c.JSON(200, gin.H{"success": true, "message": "Connection successful"})
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
	err = h.platform.Where("group_id = ? AND database_id = ?", groupID, databaseID).Assign(policy).FirstOrCreate(&policy).Error
	if err != nil {
		fail(c, 400, "POLICY_UPDATE_FAILED", "Could not save group policy")
		return
	}
	c.JSON(200, policy)
}
func (h *Handler) target(c *gin.Context) (*gorm.DB, bool) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 6*time.Second)
	defer cancel()
	db, err := h.provider.Target(ctx, c.Param("id"))
	if err != nil {
		fail(c, 502, "TARGET_UNAVAILABLE", "Target database is unavailable")
		return nil, false
	}
	return db, true
}
func (h *Handler) schemas(c *gin.Context) {
	db, ok := h.target(c)
	if !ok {
		return
	}
	defer closeTarget(db)
	var rows []struct {
		Name string `json:"name"`
	}
	err := db.Raw("SELECT schema_name AS name FROM information_schema.schemata WHERE schema_name NOT IN ('pg_catalog','information_schema') AND schema_name NOT LIKE 'pg_toast%' ORDER BY schema_name").Scan(&rows).Error
	if err != nil {
		fail(c, 502, "INTROSPECTION_FAILED", "Could not inspect target schemas")
		return
	}
	c.JSON(200, rows)
}
func (h *Handler) tables(c *gin.Context) {
	db, ok := h.target(c)
	if !ok {
		return
	}
	defer closeTarget(db)
	var rows []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	err := db.Raw("SELECT table_name AS name, table_type AS type FROM information_schema.tables WHERE table_schema = ? ORDER BY table_name", c.Param("schema")).Scan(&rows).Error
	if err != nil {
		fail(c, 502, "INTROSPECTION_FAILED", "Could not inspect target tables")
		return
	}
	c.JSON(200, rows)
}
func (h *Handler) columns(c *gin.Context)     { h.metadata(c, "columns") }
func (h *Handler) constraints(c *gin.Context) { h.metadata(c, "constraints") }
func (h *Handler) indexes(c *gin.Context)     { h.metadata(c, "indexes") }
func (h *Handler) metadata(c *gin.Context, kind string) {
	db, ok := h.target(c)
	if !ok {
		return
	}
	defer closeTarget(db)
	schema, table := c.Param("schema"), c.Param("table")
	var exists int64
	if err := db.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?", schema, table).Scan(&exists).Error; err != nil {
		fail(c, 502, "INTROSPECTION_FAILED", "Could not inspect target table")
		return
	}
	if exists == 0 {
		fail(c, 404, "TABLE_NOT_FOUND", "Schema or table not found")
		return
	}
	switch kind {
	case "columns":
		var rows []map[string]interface{}
		err := db.Raw("SELECT column_name, ordinal_position, data_type, udt_name, is_nullable, column_default FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position", schema, table).Scan(&rows).Error
		if err != nil {
			fail(c, 502, "INTROSPECTION_FAILED", "Could not inspect columns")
			return
		}
		c.JSON(200, rows)
	case "constraints":
		var rows []map[string]interface{}
		err := db.Raw("SELECT tc.constraint_name, tc.constraint_type, kcu.column_name, ccu.table_schema AS foreign_table_schema, ccu.table_name AS foreign_table_name, ccu.column_name AS foreign_column_name FROM information_schema.table_constraints tc LEFT JOIN information_schema.key_column_usage kcu ON tc.constraint_catalog=kcu.constraint_catalog AND tc.constraint_schema=kcu.constraint_schema AND tc.constraint_name=kcu.constraint_name AND tc.table_name=kcu.table_name LEFT JOIN information_schema.constraint_column_usage ccu ON ccu.constraint_catalog=tc.constraint_catalog AND ccu.constraint_schema=tc.constraint_schema AND ccu.constraint_name=tc.constraint_name WHERE tc.table_schema=? AND tc.table_name=? ORDER BY tc.constraint_name", schema, table).Scan(&rows).Error
		if err != nil {
			fail(c, 502, "INTROSPECTION_FAILED", "Could not inspect constraints")
			return
		}
		c.JSON(200, rows)
	case "indexes":
		var rows []map[string]interface{}
		err := db.Raw("SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = ? AND tablename = ? ORDER BY indexname", schema, table).Scan(&rows).Error
		if err != nil {
			fail(c, 502, "INTROSPECTION_FAILED", "Could not inspect indexes")
			return
		}
		c.JSON(200, rows)
	}
}
func (h *Handler) rows(c *gin.Context) {
	db, ok := h.target(c)
	if !ok {
		return
	}
	defer closeTarget(db)
	schema, table := c.Param("schema"), c.Param("table")
	var exists int64
	if err := db.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?", schema, table).Scan(&exists).Error; err != nil {
		fail(c, 502, "INTROSPECTION_FAILED", "Could not inspect target table")
		return
	}
	if exists == 0 {
		fail(c, 404, "TABLE_NOT_FOUND", "Schema or table not found")
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		fail(c, 400, "INVALID_PAGINATION", "offset must be zero or greater")
		return
	}
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	query := fmt.Sprintf("SELECT * FROM %s.%s LIMIT ? OFFSET ?", quote(schema), quote(table))
	var rows []map[string]interface{}
	if err := db.Raw(query, limit, offset).Scan(&rows).Error; err != nil {
		fail(c, 502, "QUERY_FAILED", "Could not read target rows")
		return
	}
	c.JSON(200, gin.H{"limit": limit, "offset": offset, "rows": rows})
}
