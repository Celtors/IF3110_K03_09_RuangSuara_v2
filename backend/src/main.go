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
	db, db_err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if db_err != nil {
		fmt.Println(db_err)
		return
	}
	if db == nil {
		return
	}

	r := gin.Default()

	r.Use(cors.Default())

		r.POST("/users", func(c *gin.Context) {
		controlpanel.CreateUserRequest(c, db)
	})

	r.GET("/users", func(c *gin.Context) {
		controlpanel.GetAllUsersRequest(c, db)
	})


	r.PATCH("/users", func(c *gin.Context) {
		controlpanel.UpdateUserRequest(c, db)
	})

	r.DELETE("/users", func(c *gin.Context) {
		controlpanel.DeleteUserRequest(c, db)
	})

	r.Run(":8080")
}