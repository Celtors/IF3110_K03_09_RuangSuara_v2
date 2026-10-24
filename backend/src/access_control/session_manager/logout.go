package sessionmanager


import (
	"backend/access_control/utility"
	"backend/access_control/models"

	"net/http"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func UnauthorizedResponse(c *gin.Context, msg string) {
	newUUID, _ := uuid.NewRandom()
	c.JSON(http.StatusUnauthorized, gin.H{
		"error": gin.H{
			"code":      "INVALID_GRANT",
			"message":   msg,
			"requestId": newUUID.String(),
		},
	})
}


func LogoutRequest(c *gin.Context, db *gorm.DB) {
	session_token, err := c.Cookie("ssid")
	if err != nil {
		UnauthorizedResponse(c, "No session found")
		return
	}

	var sessions []models.SSOSession

	db.Model(&models.SSOSession{}).
	   Where("session_token_hash = ? AND status = ?", utility.HashToken(session_token), "Active").
	   Find(&sessions)
	
	if len(sessions) != 1 {
		InternalServerResponse(c)
		return
	}


	db.Model(&models.SSOSession{}).
	   Where("session_token_hash = ?", utility.HashToken(session_token)).
	   Updates(models.SSOSession{Status: "Revoked", RevokedReason: "user_logout"})

	c.SetCookie("ssid", "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Logout successful",
	})
}