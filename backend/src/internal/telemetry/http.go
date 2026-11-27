package telemetry

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func HTTP(c *gin.Context) {
	start := time.Now()
	id := uuid.NewString()
	ctx := c.Request.Context()
	// Tracestate is opaque and can carry arbitrary client data into span exports.
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{"traceparent": c.Request.Header.Get("Traceparent")})
	ctx = With(ctx, slog.String("correlation_id", id), ID("database_id", c.Param("id")))
	method, route := httpMethod(c.Request.Method), c.FullPath()
	spanName, spanMethod := method, method
	if method == "OTHER" {
		spanName, spanMethod = "HTTP", "_OTHER"
	}
	if route != "" {
		spanName += " " + route
	}
	ctx, span := Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("http.request.method", spanMethod)))
	defer EndSpan(span)
	if route != "" {
		span.SetAttributes(attribute.String("http.route", route))
	}
	c.Request = c.Request.WithContext(ctx)
	c.Header("X-Correlation-ID", id)

	defer func() {
		ctx := c.Request.Context()
		panicked := recover() != nil
		if panicked {
			slog.ErrorContext(ctx, "http.panic", "status", "failed", "route", route)
			c.AbortWithStatus(http.StatusInternalServerError)
		}
		status := c.Writer.Status()
		span.SetAttributes(attribute.Int("http.response.status_code", status))
		switch {
		case panicked:
			spanPanic(span)
		case status >= 500:
			span.SetAttributes(attribute.String("error.type", strconv.Itoa(status)))
			span.SetStatus(codes.Error, "")
		}
		level, outcome := slog.LevelInfo, "succeeded"
		switch {
		case panicked || status >= 500:
			level, outcome = slog.LevelError, "failed"
		case status >= 400:
			level, outcome = slog.LevelWarn, "rejected"
		}
		elapsed := time.Since(start)
		metricRoute := route
		if metricRoute == "" {
			metricRoute = "unmatched"
		}
		statusClass := strconv.Itoa(status/100) + "xx"
		httpRequests.WithLabelValues(method, metricRoute, statusClass).Inc()
		httpDuration.WithLabelValues(method, metricRoute, statusClass).Observe(elapsed.Seconds())
		slog.Log(ctx, level, "http.request", "status", outcome, "http_status", status,
			"method", method, "route", route,
			"duration_ms", float64(elapsed.Microseconds())/1000)
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
