package controlpanel

import (
	"net/http"
	"backend/access_control/models"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type UserGroupPayload struct {
	UserId  uuid.UUID `json:"user_id" binding:"required"`
	GroupId uuid.UUID `json:"group_id" binding:"required"`
}

type UserFromGroupPayload struct {
	UserId   uuid.UUID `json:"user_id"`
	Name     string            `json:"name"`
	Email    string            `json:"email"`
	Status   string			   `json:"status"`
	PlacedAt time.Time         `json:"placed_at"`
}

type GroupFromUserPayload struct {
	GroupId     uuid.UUID `json:"group_id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	PlacedAt    time.Time         `json:"placed_at"`
}

func (payload UserGroupPayload) GetDBStructCreate() (models.UserGroup) {
	return models.UserGroup{
		UserId:  payload.UserId,
		GroupId: payload.GroupId,
	}
}

func AddUserToGroup(c *gin.Context, db *gorm.DB) {
	var userGroupPayload UserGroupPayload

	if err := c.ShouldBindJSON(&userGroupPayload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to add user to group: " + err.Error(),
		})
		return
	}

	userGroupModel := userGroupPayload.GetDBStructCreate()


	if err := db.Create(&userGroupModel).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to add user to group: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"status":  "success",
		"message": "Successfully added user to group",
	})
}

func RemoveUserFromGroup(c *gin.Context, db *gorm.DB) {
	var userGroupPayload UserGroupPayload

	if err := c.ShouldBindJSON(&userGroupPayload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to remove user from group: " + err.Error(),
		})
		return
	}

	db.Where("user_id = ? AND group_id = ?", userGroupPayload.UserId, userGroupPayload.GroupId).Delete(&models.UserGroup{})

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Successfully removed user to group",
	})
}

func GetUsersFromGroupRequest(c *gin.Context, db *gorm.DB) {
	var usersFromGroup []UserFromGroupPayload

	var groupPKey PKeyPayload

	if err := c.ShouldBindJSON(&groupPKey); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to remove user from group: " + err.Error(),
		})
		return
	}

	db.Model(&models.UserGroup{}).
		Select("user_groups.user_id, users.name, users.email, users.status, user_groups.created_at AS placed_at").
		Joins("LEFT JOIN users ON users.id = user_groups.user_id").
		Where("group_id = ?", groupPKey.Id).
		Find(&usersFromGroup)

	c.JSON(http.StatusOK, usersFromGroup)
}

func GetGroupsFromUserRequest(c *gin.Context, db *gorm.DB) {
	var groupFromUsers []GroupFromUserPayload

	var userPKey PKeyPayload

	if err := c.ShouldBindJSON(&userPKey); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to remove user from group: " + err.Error(),
		})
		return
	}

	db.Model(&models.UserGroup{}).
		Select("user_groups.group_id, groups_.name, groups_.description, user_groups.created_at AS placed_at").
		Joins("LEFT JOIN groups_ ON user_groups.group_id = groups_.id").
		Where("user_groups.user_id = ?", userPKey.Id).
		Find(&groupFromUsers)

	c.JSON(http.StatusOK, groupFromUsers)
}