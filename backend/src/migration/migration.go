//go:build !migrationdev

package migration

import (
	"context"
	"github.com/gin-gonic/gin"
)


func MigrationService(_ context.Context, repository *Repository) (*Service, func(), error) {
	return &Service{Repository: repository}, func() {}, nil
}

// /api/session belongs to Access Control.
func RegisterMigrationSession(_ *gin.Engine, _ *Service) {}
func ValidateListenAddress(_ string) error                         { return nil }
