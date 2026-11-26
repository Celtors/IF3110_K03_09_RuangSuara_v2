package telemetry

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func HTTP(c *gin.Context) {
	start := time.Now()
	id := uuid.NewString()
	ctx := With(c.Request.Context(), slog.String("correlation_id", id), ID("database_id", c.Param("id")))
	c.Request = c.Request.WithContext(ctx)
	c.Header("X-Correlation-ID", id)

	defer func() {
		ctx := c.Request.Context()
		panicked := recover() != nil
		if panicked {
			slog.ErrorContext(ctx, "http.panic", "status", "failed", "route", c.FullPath())
			c.AbortWithStatus(http.StatusInternalServerError)
		}
		status := c.Writer.Status()
		level, outcome := slog.LevelInfo, "succeeded"
		switch {
		case panicked || status >= 500:
			level, outcome = slog.LevelError, "failed"
		case status >= 400:
			level, outcome = slog.LevelWarn, "rejected"
		}
		slog.Log(ctx, level, "http.request", "status", outcome, "http_status", status,
			"method", httpMethod(c.Request.Method), "route", c.FullPath(),
			"duration_ms", float64(time.Since(start).Microseconds())/1000)
	}()
	c.Next()
}

func httpMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}
