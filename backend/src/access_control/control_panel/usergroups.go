package controlpanel

import (
	"backend/access_control/models"
	"errors"
	"net/http"
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
	Username     string        `json:"username"`
	Status   string			   `json:"status"`
	PlacedAt time.Time         `json:"placed_at"`
}

type GroupFromUserPayload struct {
	GroupId     uuid.UUID `json:"group_id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	PlacedAt    time.Time         `json:"placed_at"`
}

type GroupLeaderPayload struct {
	UserId   uuid.UUID `json:"user_id"`
	Username     string        `json:"username"`
}

func (payload UserGroupPayload) GetDBStructCreate() (models.UserGroup) {
	return models.UserGroup{
		UserId:  payload.UserId,
		GroupId: payload.GroupId,
	}
}

func (payload UserGroupPayload) GetLeaderStruct() (models.GroupLeader) {
	return models.GroupLeader{
		LeaderId:  payload.UserId,
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

	group_id, exists := c.GetQuery("id")

	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to get users from group",
		})
		return
	}

	db.Model(&models.UserGroup{}).
		Select("user_groups.user_id, users.username, users.status, user_groups.created_at AS placed_at").
		Joins("LEFT JOIN users ON users.id = user_groups.user_id").
		Where("group_id = ?", group_id).
		Find(&usersFromGroup)

	c.JSON(http.StatusOK, usersFromGroup)
}

func GetGroupsFromUserRequest(c *gin.Context, db *gorm.DB) {
	var groupFromUsers []GroupFromUserPayload

	user_id, exists := c.GetQuery("id")

	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to get groups from user",
		})
		return
	}

	db.Model(&models.UserGroup{}).
		Select("user_groups.group_id, groups_.name, groups_.description, user_groups.created_at AS placed_at").
		Joins("LEFT JOIN groups_ ON user_groups.group_id = groups_.id").
		Where("user_groups.user_id = ?", user_id).
		Find(&groupFromUsers)

	c.JSON(http.StatusOK, groupFromUsers)
}

func SetGroupLeader(c *gin.Context, db *gorm.DB) {
	var userGroupPayload UserGroupPayload

	if err := c.ShouldBindJSON(&userGroupPayload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to set leader to group: " + err.Error(),
		})
		return
	}

	groupLeaderModel := userGroupPayload.GetLeaderStruct()

	var leader models.GroupLeader
	err := db.Where("group_id = ?", groupLeaderModel.GroupId).Take(&leader).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := db.Create(&groupLeaderModel).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "Failed to set leader to group: " + err.Error(),
			})
			return
		}
	} else if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to set leader to group: " + err.Error(),
		})
		return
	} else {
		err2 := db.Model(models.GroupLeader{}).
				Where("group_id = ?", groupLeaderModel.GroupId).
				Update("leader_id", groupLeaderModel.LeaderId).Error
		if err2 != nil {
			c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to set leader to group: " + err2.Error(),
		})
		return
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"status":  "success",
		"message": "Successfully set user to be leader of group",
	})
}


func GetGroupLeader(c *gin.Context, db *gorm.DB) {
	var groupLeader []GroupLeaderPayload

	group_id, exists := c.GetQuery("id")

	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Failed to get leader from group",
		})
		return
	}

	db.Model(&models.GroupLeader{}).
		Select("group_leaders.user_id, users.username").
		Joins("LEFT JOIN users ON users.id = group_leaders.leader_id").
		Where("group_id = ?", group_id).
		Find(&groupLeader)
	
	if len(groupLeader) < 1 {
		c.JSON(http.StatusNotFound, gin.H{
			"status":  "Not Found",
			"message": "Leader not found",
		})
	}

	c.JSON(http.StatusOK, groupLeader[0])
}