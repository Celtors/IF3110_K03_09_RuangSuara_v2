//go:build !migrationdev

package main

import (
	"context"

	"backend/migration"
	"github.com/gin-gonic/gin"
)


func migrationService(_ context.Context, repository *migration.Repository) (*migration.Service, func(), error) {
	return &migration.Service{Repository: repository}, func() {}, nil
}

// /api/session belongs to Access Control.
func registerMigrationSession(_ *gin.Engine, _ *migration.Service) {}
func validateListenAddress(_ string) error                         { return nil }
