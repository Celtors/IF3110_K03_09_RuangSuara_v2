package migration

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes attaches to the application's existing router. It never creates an engine or listener.
// Authentication and authorization fail closed.
func RegisterRoutes(router gin.IRouter, s *Service) {
	g := router.Group("/api/databases/:id")
	g.Use(func(c *gin.Context) {
		if s.Access == nil {
			respond(c, nil, fault("unauthenticated", "Authentication is not configured"))
			c.Abort()
			return
		}
		user, err := s.Access.Identity(c.Request)
		if err != nil || user == "" {
			respond(c, nil, fault("unauthenticated", "A validated session is required"))
			c.Abort()
			return
		}
		if c.Request.Method != http.MethodGet {
			if err = s.Access.CSRF(c.Request); err != nil {
				respond(c, nil, fault("csrf_failed", "CSRF token or request origin is invalid"))
				c.Abort()
				return
			}
		}
		c.Set("migration_user", user)
		c.Next()
	})
	g.GET("/migrations", func(c *gin.Context) {
		ms, p, e := s.History(c.Request.Context(), c.GetString("migration_user"), c.Param("id"))
		respond(c, gin.H{"migrations": ms, "can_edit": p >= Edit}, e)
	})
	g.GET("/migrations/:migrationId", func(c *gin.Context) {
		m, e := s.Details(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), c.Param("migrationId"))
		respond(c, m, e)
	})
	g.GET("/migrations/:migrationId/attempts", func(c *gin.Context) {
		m, e := s.Details(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), c.Param("migrationId"))
		respond(c, gin.H{"attempts": m.Attempts}, e)
	})
	save := func(c *gin.Context) {
		var d Draft
		if !decode(c, &d) {
			return
		}
		m, e := s.Save(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), c.Param("migrationId"), d)
		if e == nil && c.Request.Method == http.MethodPost {
			c.JSON(http.StatusCreated, m)
			return
		}
		respond(c, m, e)
	}
	g.POST("/migrations", save)
	g.PATCH("/migrations/:migrationId", save)
	g.POST("/migrations/:migrationId/execute", func(c *gin.Context) {
		a, e := s.Execute(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), c.Param("migrationId"))
		respond(c, gin.H{"attempt": a}, e)
	})
	g.POST("/migrations/reconcile", func(c *gin.Context) {
		e := s.Reconcile(c.Request.Context(), c.GetString("migration_user"), c.Param("id"))
		respond(c, gin.H{"reconciled": e == nil}, e)
	})
	g.GET("/schema", func(c *gin.Context) {
		name := c.DefaultQuery("name", "public")
		schema, e := s.Schema(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), name)
		respond(c, schema, e)
	})
	g.POST("/schema-diff", func(c *gin.Context) {
		var body struct {
			Desired Schema `json:"desired"`
		}
		if !decode(c, &body) {
			return
		}
		diff, e := s.Diff(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), body.Desired)
		respond(c, diff, e)
	})
	g.POST("/query-plan", func(c *gin.Context) {
		var body struct {
			SQL string `json:"sql_script"`
		}
		if !decode(c, &body) {
			return
		}
		plan, e := s.Plan(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), body.SQL)
		respond(c, plan, e)
	})
}
func decode(c *gin.Context, out any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		respond(c, nil, fault("invalid_input", "Expected a valid JSON object with documented fields"))
		return false
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		respond(c, nil, fault("invalid_input", "Expected exactly one JSON object"))
		return false
	}
	return true
}
func respond(c *gin.Context, data any, err error) {
	if err == nil {
		c.JSON(http.StatusOK, data)
		return
	}
	var f *Fault
	if !errors.As(err, &f) {
		f = &Fault{"internal_error", "The migration operation could not be completed"}
	}
	statuses := map[string]int{"unauthenticated": 401, "forbidden": 403, "csrf_failed": 403, "not_found": 404, "invalid_input": 400, "invalid_sql": 422, "unsupported_sql": 422, "conflict": 409, "already_applied": 409, "indeterminate": 409, "execution_failed": 422, "database_unavailable": 503, "authorization_unavailable": 503}
	status, ok := statuses[f.Code]
	if !ok {
		status = 500
	}
	c.JSON(status, gin.H{"error": f, "data": data})
}
