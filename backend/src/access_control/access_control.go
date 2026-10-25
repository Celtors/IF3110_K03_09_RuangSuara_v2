package accesscontrol

import (
	"errors"
	"os"
	"time"

	dbmanagement "backend/database_management"

	controlpanel "backend/access_control/control_panel"
	sessionmanager "backend/access_control/session_manager"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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
	access_control_db, db_err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
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
	dbmanagement.RegisterRoutes(r, access_control_db)
	admin := r.Group("/admin-control-panel", dbmanagement.PlatformAdminMiddlewares(access_control_db)...)

	admin.POST("/users", func(c *gin.Context) {
		controlpanel.CreateUserRequest(c, access_control_db)
	})

	admin.GET("/users", func(c *gin.Context) {
		controlpanel.GetAllUsersRequest(c, access_control_db)
	})

	admin.PATCH("/users", func(c *gin.Context) {
		controlpanel.UpdateUserRequest(c, access_control_db)
	})

	admin.DELETE("/users", func(c *gin.Context) {
		controlpanel.DeleteUserRequest(c, access_control_db)
	})

	admin.POST("/groups", func(c *gin.Context) {
		controlpanel.CreateGroupRequest(c, access_control_db)
	})

	admin.GET("/groups", func(c *gin.Context) {
		controlpanel.GetAllGroupsRequest(c, access_control_db)
	})

	admin.PATCH("/groups", func(c *gin.Context) {
		controlpanel.UpdateGroupRequest(c, access_control_db)
	})

	admin.DELETE("/groups", func(c *gin.Context) {
		controlpanel.DeleteGroupRequest(c, access_control_db)
	})

	admin.POST("/users/groups", func(c *gin.Context) {
		controlpanel.AddUserToGroup(c, access_control_db)
	})

	admin.GET("/users/groups", func(c *gin.Context) {
		controlpanel.GetGroupsFromUserRequest(c, access_control_db)
	})

	admin.DELETE("/users/groups", func(c *gin.Context) {
		controlpanel.RemoveUserFromGroup(c, access_control_db)
	})

	admin.POST("/groups/users", func(c *gin.Context) {
		controlpanel.AddUserToGroup(c, access_control_db)
	})

	admin.GET("/groups/users", func(c *gin.Context) {
		controlpanel.GetUsersFromGroupRequest(c, access_control_db)
	})

	admin.DELETE("/groups/users", func(c *gin.Context) {
		controlpanel.RemoveUserFromGroup(c, access_control_db)
	})

	admin.GET("/databases", func(c *gin.Context) {
		controlpanel.GetAllDatabasesRequest(c, access_control_db)
	})

	admin.POST("/policy", func(c *gin.Context) {
		controlpanel.AddGroupPolicyRequest(c, access_control_db)
	})

	admin.GET("/policy", func(c *gin.Context) {
		controlpanel.GetPoliciesRequest(c, access_control_db)
	})

	admin.PATCH("/policy", func(c *gin.Context) {
		controlpanel.UpdateGroupPolicyRequest(c, access_control_db)
	})

	admin.DELETE("/policy", func(c *gin.Context) {
		controlpanel.RemoveGroupPolicyRequest(c, access_control_db)
	})

	r.POST("session-manager/login", func(c *gin.Context) {
		sessionmanager.LoginRequest(c, access_control_db)
	})

	r.POST("session-manager/logout", func(c *gin.Context) {
		sessionmanager.LogoutRequest(c, access_control_db)
	})
	return access_control_db, nil
}
