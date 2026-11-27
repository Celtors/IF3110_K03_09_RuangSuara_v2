package app

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"backend/migration"
	"github.com/gin-gonic/gin"
)

// RegisterMigration adds Migration APIs and pages to the shared server.
func RegisterMigration(router *gin.Engine, service *migration.Service, assets string) {
	r := router.Group("")
	r.Use(func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "same-origin")
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	migration.RegisterRoutes(r, service)
	// The whole frontend is one single-page app. Every screen URL answers with the same shell
	// (index.html) and the client router picks the screen from the path and query, so a refresh
	// or a pasted link works. The old .html names are served too; the client replaces them with
	// the clean URL.
	shell := func(c *gin.Context) { c.File(filepath.Join(assets, "index.html")) }
	screens := []string{"/", "/login", "/home", "/migrations", "/migrations/"}
	legacy := []string{"/index.html", "/login.html", "/home.html", "/migrations/index.html"}
	for _, section := range []string{"users", "groups", "databases", "policies"} {
		screens = append(screens, "/admin/"+section)
		legacy = append(legacy, "/admin/"+section+".html")
	}
	for _, page := range []string{"history", "editor", "details", "schema-diff", "query-plan", "change-request"} {
		screens = append(screens, "/migrations/"+page)
		legacy = append(legacy, "/migrations/"+page+".html")
	}
	for _, path := range append(screens, legacy...) {
		r.GET(path, shell)
	}
	if _, err := os.Stat(filepath.Join(assets, "assets")); err == nil {
		r.Static("/assets", filepath.Join(assets, "assets"))
	}
}
