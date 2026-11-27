package migration

import (
	"errors"
	"net/http"
	"strconv"

	"backend/internal/telemetry"

	"github.com/gin-gonic/gin"
)

// RegisterReviewRoutes attaches the approval workflow to the application's router.
//
//	POST /api/databases/:id/change-requests                       submit {migration_id, operation}
//	GET  /api/databases/:id/change-requests                       list (?migration_id=&status=&limit=)
//	GET  /api/databases/:id/change-requests/:requestId            details, approvals and audit events
//	POST /api/databases/:id/change-requests/:requestId/approve    {comment?}
//	POST /api/databases/:id/change-requests/:requestId/reject     {comment}  (reason required)
//	POST /api/databases/:id/change-requests/:requestId/cancel     {comment?}
//	GET  /api/change-requests/pending-approval                    requests waiting for the caller
//
// Authentication, CSRF and authorization fail closed, like the Migration routes.
func RegisterReviewRoutes(router gin.IRouter, r *Review) {
	s := r.Service
	RegisterJobRoutes(router, r)

	g := router.Group("/api/databases/:id/change-requests")
	g.Use(reviewSecurityHeaders(), requireSession(s))

	g.POST("", func(c *gin.Context) {
		var in SubmitRequest
		if !decode(c, &in) {
			return
		}
		q, err := r.Submit(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), in)
		if err == nil {
			c.JSON(http.StatusCreated, gin.H{"change_request": q, "job_id": q.ID})
			return
		}
		respond(c, nil, err)
	})
	g.GET("", func(c *gin.Context) {
		limit := 0
		if v := c.Query("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				respond(c, nil, fault("invalid_input", "limit must be a number"))
				return
			}
			limit = n
		}
		list, err := r.List(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), c.Query("migration_id"), c.Query("status"), limit)
		respond(c, gin.H{"change_requests": list}, err)
	})
	g.GET("/:requestId", func(c *gin.Context) {
		q, err := r.Get(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), c.Param("requestId"))
		respond(c, gin.H{"change_request": q}, err)
	})
	g.POST("/:requestId/approve", func(c *gin.Context) {
		var body DecisionBody
		if !decodeOptional(c, &body) {
			return
		}
		q, err := r.Decide(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), c.Param("requestId"), true, body.Comment)
		respond(c, gin.H{"change_request": q}, err)
	})
	g.POST("/:requestId/reject", func(c *gin.Context) {
		var body DecisionBody
		if !decode(c, &body) {
			return
		}
		q, err := r.Decide(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), c.Param("requestId"), false, body.Comment)
		respond(c, gin.H{"change_request": q}, err)
	})
	g.POST("/:requestId/cancel", func(c *gin.Context) {
		var body DecisionBody
		if !decodeOptional(c, &body) {
			return
		}
		q, err := r.Cancel(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), c.Param("requestId"), body.Comment)
		respond(c, gin.H{"change_request": q}, err)
	})

	inbox := router.Group("/api/change-requests")
	inbox.Use(reviewSecurityHeaders(), requireSession(s))
	inbox.GET("/pending-approval", func(c *gin.Context) {
		list, err := r.PendingApprovals(c.Request.Context(), c.GetString("migration_user"))
		respond(c, gin.H{"change_requests": list}, err)
	})
}

func reviewSecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "same-origin")
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

// requireSession mirrors the Migration routes: a validated session, and a valid CSRF
// token for every request that is not a GET.
func requireSession(s *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.Access == nil {
			respond(c, nil, fault("unauthenticated", "Authentication is not configured"))
			c.Abort()
			return
		}
		user, err := s.Access.Identity(c)
		if err != nil || user == "" {
			var f *Fault
			if errors.As(err, &f) && (f.Code == "authorization_unavailable" || f.Code == "csrf_failed") {
				respond(c, nil, f)
			} else {
				respond(c, nil, fault("unauthenticated", "A validated session is required"))
			}
			c.Abort()
			return
		}
		ctx := telemetry.With(c.Request.Context(), telemetry.ID("actor_id", user),
			telemetry.ID("migration_id", c.Param("migrationId")), telemetry.ID("job_id", c.Param("requestId")))
		c.Request = c.Request.WithContext(ctx)
		c.Set("migration_user", user)
		c.Next()
	}
}

// decodeOptional accepts an empty body (the comment is optional) or one JSON object.
func decodeOptional(c *gin.Context, out any) bool {
	if c.Request.ContentLength == 0 {
		return true
	}
	return decode(c, out)
}
