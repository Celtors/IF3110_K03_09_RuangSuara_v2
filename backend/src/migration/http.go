package migration

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes attaches to the application's existing router. It never creates an engine or listener.
// Authentication and authorization fail closed.
func RegisterRoutes(router gin.IRouter, s *Service) {
	g := router.Group("/api/databases/:id")
	g.Use(requireSession(s))
	g.GET("/migration-history", func(c *gin.Context) {
		hideRollback := false
		if value, present := c.GetQuery("hide_rollback"); present {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				respond(c, nil, fault("invalid_input", "hide_rollback must be a boolean"))
				return
			}
			hideRollback = parsed
		}
		history, err := s.ExecutionHistory(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), hideRollback)
		respond(c, gin.H{"attempts": history}, err)
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
		// Every execution needs approval from the leader of each group with access, so direct
		// execution is closed. MIGRATION_ALLOW_DIRECT_EXECUTE=true restores it for local development only.
		if os.Getenv("MIGRATION_ALLOW_DIRECT_EXECUTE") != "true" {
			respond(c, nil, fault("approval_required", "Submit a change request; it runs after every required group leader approves"))
			return
		}
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

	g.POST("/query", func(c *gin.Context) {
		var body QueryRequest
		if !decode(c, &body) {
			return
		}
		result, e := s.Query(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), body.Query)
		if e != nil {
			respond(c, gin.H{"execution_time_ms": result.ExecutionTimeMS, "postgres_code": result.PostgresCode, "position": result.Position}, e)
			return
		}
		respond(c, result, nil)
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
	statuses := map[string]int{"unauthenticated": 401, "forbidden": 403, "csrf_failed": 403, "not_found": 404, "invalid_input": 400, "invalid_sql": 422, "unsupported_sql": 422, "conflict": 409, "approval_required": 409, "already_applied": 409, "migration_order_conflict": 409, "indeterminate": 409, "execution_failed": 422, "database_unavailable": 503, "authorization_unavailable": 503, "query_failed": 422, "query_timeout": 504}
	status, ok := statuses[f.Code]
	if !ok {
		status = 500
	}
	slog.WarnContext(c.Request.Context(), "migration.request_failed", "status", "failed",
		"reason", f.Code, "route", c.FullPath())
	c.JSON(status, gin.H{"error": f, "data": data})
}
