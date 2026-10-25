package controlpanel

import (
	"backend/access_control/models"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetAllDatabasesRequest(c *gin.Context, db *gorm.DB) {
	var dbs []models.Database

	if err := db.Find(&dbs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": "Failed to get user data: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, dbs)
}
