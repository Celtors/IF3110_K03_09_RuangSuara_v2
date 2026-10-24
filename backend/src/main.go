package main

import (
	"fmt"

	controlpanel "backend/access_control/control_panel"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {

	dsn := "host=localhost user=myuser password=mypassword dbname=access-control-db port=9000 sslmode=disable TimeZone=Asia/Jakarta"
	access_control_db, db_err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if db_err != nil {
		fmt.Println(db_err)
		return
	}
	if access_control_db == nil {
		return
	}

	r := gin.Default()

	r.Use(cors.Default())

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

	r.Run(":8080")
}