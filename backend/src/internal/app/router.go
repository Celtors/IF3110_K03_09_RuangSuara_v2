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
	pages := []string{"index.html", "history.html", "editor.html", "details.html", "schema-diff.html", "query-plan.html"}
	for _, page := range pages {
		r.GET("/migrations/"+page, func(c *gin.Context) { c.File(filepath.Join(assets, "migrations", page)) })
	}
	// Serve the page directly to avoid an index.html redirect loop.
	r.GET("/migrations/", func(c *gin.Context) { c.File(filepath.Join(assets, "migrations", "index.html")) })
	r.GET("/", func(c *gin.Context) { c.File(filepath.Join(assets, "index.html")) })
	r.GET("/index.html", func(c *gin.Context) { c.File(filepath.Join(assets, "index.html")) })
	for _, page := range []string{"login.html", "home.html", "admin/users.html", "admin/groups.html", "admin/databases.html", "admin/policies.html"} {
		r.GET("/"+page, func(c *gin.Context) { c.File(filepath.Join(assets, page)) })
	}
	if _, err := os.Stat(filepath.Join(assets, "assets")); err == nil {
		r.Static("/assets", filepath.Join(assets, "assets"))
	}
}
