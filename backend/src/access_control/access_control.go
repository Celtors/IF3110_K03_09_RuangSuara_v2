package accesscontrol

import (
	"fmt"

	controlpanel "backend/access_control/control_panel"
	sessionmanager "backend/access_control/session_manager"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func SetupAccessControl(r *gin.Engine) {
	dsn := "host=localhost user=myuser password=mypassword dbname=access-control-db port=9000 sslmode=disable TimeZone=Asia/Jakarta"
	access_control_db, db_err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if db_err != nil {
		fmt.Println(db_err)
		return
	}
	if access_control_db == nil {
		return
	}

	r.POST("admin-control-panel/users", func(c *gin.Context) {
		controlpanel.CreateUserRequest(c, access_control_db)
	})

	r.GET("admin-control-panel/users", func(c *gin.Context) {
		controlpanel.GetAllUsersRequest(c, access_control_db)
	})

	r.PATCH("admin-control-panel/users", func(c *gin.Context) {
		controlpanel.UpdateUserRequest(c, access_control_db)
	})

	r.DELETE("admin-control-panel/users", func(c *gin.Context) {
		controlpanel.DeleteUserRequest(c, access_control_db)
	})

	r.POST("admin-control-panel/groups", func(c *gin.Context) {
		controlpanel.CreateGroupRequest(c, access_control_db)
	})

	r.GET("admin-control-panel/groups", func(c *gin.Context) {
		controlpanel.GetAllGroupsRequest(c, access_control_db)
	})

	r.PATCH("admin-control-panel/groups", func(c *gin.Context) {
		controlpanel.UpdateGroupRequest(c, access_control_db)
	})

	r.DELETE("admin-control-panel/groups", func(c *gin.Context) {
		controlpanel.DeleteGroupRequest(c, access_control_db)
	})

	r.POST("admin-control-panel/users/groups", func(c *gin.Context) {
		controlpanel.AddUserToGroup(c, access_control_db)
	})

	r.GET("admin-control-panel/users/groups", func(c *gin.Context) {
		controlpanel.GetGroupsFromUserRequest(c, access_control_db)
	})

	r.DELETE("admin-control-panel/users/groups", func(c *gin.Context) {
		controlpanel.RemoveUserFromGroup(c, access_control_db)
	})

	r.POST("admin-control-panel/groups/users", func(c *gin.Context) {
		controlpanel.AddUserToGroup(c, access_control_db)
	})

	r.GET("admin-control-panel/groups/users", func(c *gin.Context) {
		controlpanel.GetUsersFromGroupRequest(c, access_control_db)
	})

	r.DELETE("admin-control-panel/groups/users", func(c *gin.Context) {
		controlpanel.RemoveUserFromGroup(c, access_control_db)
	})

	r.GET("admin-control-panel/databases", func(c *gin.Context) {
		controlpanel.GetAllDatabasesRequest(c, access_control_db)
	})

	r.POST("admin-control-panel/policy", func(c *gin.Context) {
		controlpanel.AddGroupPolicyRequest(c, access_control_db)
	})

	r.GET("admin-control-panel/policy", func(c *gin.Context) {
		controlpanel.GetPoliciesRequest(c, access_control_db)
	})

	r.PATCH("admin-control-panel/policy", func(c *gin.Context) {
		controlpanel.UpdateGroupPolicyRequest(c, access_control_db)
	})

	r.DELETE("admin-control-panel/policy", func(c *gin.Context) {
		controlpanel.RemoveGroupPolicyRequest(c, access_control_db)
	})

	r.POST("session-manager/login", func(c *gin.Context) {
		sessionmanager.LoginRequest(c, access_control_db)
	})

	r.POST("session-manager/logout", func(c *gin.Context) {
		sessionmanager.LogoutRequest(c, access_control_db)
	})

	r.GET("access-control/users", func(c *gin.Context) {
		UserAuthorizedRequest(c, access_control_db)
	})
}
