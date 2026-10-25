package accesscontrol

import (
	"backend/access_control/utility"
	"backend/access_control/models"

	"net/http"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func UserAuthorizedRequest(c *gin.Context, db *gorm.DB) {
	database_id_str, exists := c.GetQuery("databaseId")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No query found"})
		return
	}

	database_id, err := uuid.Parse(database_id_str)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid query value"})
		return
	}

	session_token, err := c.Cookie("ssid")
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No session found"})
		return
	}

	var sessions []models.SSOSession

	db.Model(&models.SSOSession{}).
	   Where("session_token_hash = ? AND status = ?", utility.HashToken(session_token), "Active").
	   Find(&sessions)
	
	if len(sessions) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No Active session found"})
		return
	}

	session := sessions[0]

	var user []models.User
	db.Model(&models.User{}).
	   Where("id = ?", session.UserId).
	   Find(&user)

	authLevel := user[0].GetHighestAuthorization(database_id, db)

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"effect": authLevel,
	})
}