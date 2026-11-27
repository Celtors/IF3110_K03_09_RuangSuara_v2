package accesscontrol

import (
	"context"
	"errors"
	"os"
	"time"

	controlpanel "backend/access_control/control_panel"
	sessionmanager "backend/access_control/session_manager"
	dbmanagement "backend/database_management"
	"backend/internal/telemetry"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// PlatformDSN supplies one platform database configuration to every module.
func PlatformDSN() string {
	dsn := os.Getenv("PLATFORM_DATABASE_DSN")
	if dsn == "" {
		dsn = os.Getenv("PLATFORM_DATABASE_URL")
	}
	if dsn == "" {
		dsn = "host=localhost user=myuser password=mypassword dbname=access-control-db port=9000 sslmode=disable TimeZone=Asia/Jakarta"
	}
	return dsn
}

func SetupAccessControl(r *gin.Engine) (*gorm.DB, error) {
	dsn := PlatformDSN()
	access_control_db, db_err := gorm.Open(postgres.New(postgres.Config{
		DSN: dsn, OptionOpenDB: []stdlib.OptionOpenDB{telemetry.TraceStdlib("platform")},
	}), &gorm.Config{Logger: logger.Discard})
	if db_err != nil {
		return nil, errors.New("could not open platform database")
	}
	if access_control_db == nil {
		return nil, errors.New("platform database is unavailable")
	}
	if sqlDB, err := access_control_db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(10)
		sqlDB.SetMaxIdleConns(3)
		sqlDB.SetConnMaxLifetime(30 * time.Minute)
	}
	initCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sessionmanager.Initialize(initCtx, access_control_db); err != nil {
		if sqlDB, dbErr := access_control_db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
		return nil, errors.New("could not initialize session storage")
	}

	dbmanagement.RegisterRoutes(r, access_control_db)
	admin := r.Group("/admin-control-panel", dbmanagement.PlatformAdminMiddlewares(access_control_db)...)
	withPlatform := func(handler func(*gin.Context, *gorm.DB)) gin.HandlerFunc {
		return func(c *gin.Context) {
			handler(c, access_control_db.WithContext(context.WithoutCancel(c.Request.Context())))
		}
	}

	admin.POST("/users", withPlatform(controlpanel.CreateUserRequest))

	admin.GET("/users", withPlatform(controlpanel.GetAllUsersRequest))

	admin.PATCH("/users", withPlatform(controlpanel.UpdateUserRequest))

	admin.DELETE("/users", withPlatform(controlpanel.DeleteUserRequest))

	admin.POST("/groups", withPlatform(controlpanel.CreateGroupRequest))

	admin.GET("/groups", withPlatform(controlpanel.GetAllGroupsRequest))

	admin.PATCH("/groups", withPlatform(controlpanel.UpdateGroupRequest))

	admin.DELETE("/groups", withPlatform(controlpanel.DeleteGroupRequest))

	admin.POST("/users/groups", withPlatform(controlpanel.AddUserToGroup))

	admin.GET("/users/groups", withPlatform(controlpanel.GetGroupsFromUserRequest))

	admin.DELETE("/users/groups", withPlatform(controlpanel.RemoveUserFromGroup))

	admin.POST("/groups/users", withPlatform(controlpanel.AddUserToGroup))

	admin.GET("/groups/users", withPlatform(controlpanel.GetUsersFromGroupRequest))

	admin.DELETE("/groups/users", withPlatform(controlpanel.RemoveUserFromGroup))

	admin.GET("/groups/leader", withPlatform(controlpanel.GetGroupLeader))

	admin.POST("/groups/leader", withPlatform(controlpanel.SetGroupLeader))

	admin.GET("/databases", withPlatform(controlpanel.GetAllDatabasesRequest))

	admin.POST("/policy", withPlatform(controlpanel.AddGroupPolicyRequest))

	admin.GET("/policy", withPlatform(controlpanel.GetPoliciesRequest))

	admin.PATCH("/policy", withPlatform(controlpanel.UpdateGroupPolicyRequest))

	admin.DELETE("/policy", withPlatform(controlpanel.RemoveGroupPolicyRequest))

	r.POST("session-manager/login", func(c *gin.Context) {
		sessionmanager.LoginRequest(c, access_control_db)
	})

	r.POST("session-manager/logout", func(c *gin.Context) {
		sessionmanager.LogoutRequest(c, access_control_db)
	})
	return access_control_db, nil
}
