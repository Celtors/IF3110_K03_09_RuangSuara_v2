package main

import (
	accesscontrol "backend/access_control"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()

	r.Use(cors.Default())

	accesscontrol.SetupAccessControl(r)

	r.Run(":8080")
}
